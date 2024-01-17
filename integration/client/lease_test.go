/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package client

import (
	"runtime"
	"testing"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	imagelist "github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/errdefs"
	"github.com/opencontainers/image-spec/identity"
)

func TestLeaseResources(t *testing.T) {
	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	snapshotterName := "native"
	if runtime.GOOS == "windows" {
		snapshotterName = "windows"
	}
	var (
		ls     = client.LeasesService()
		cs     = client.ContentStore()
		imgSrv = client.ImageService()
		sn     = client.SnapshotService(snapshotterName)
	)

	l, err := ls.Create(ctx, leases.WithRandomID())
	if err != nil {
		t.Fatal(err)
	}
	defer ls.Delete(ctx, l, leases.SynchronousDelete)

	// step 1: download image
	imageName := imagelist.Get(imagelist.Pause)

	image, err := client.Pull(ctx, imageName, WithPullUnpack, WithPullSnapshotter(snapshotterName))
	if err != nil {
		t.Fatal(err)
	}
	defer imgSrv.Delete(ctx, imageName)

	// both the config and snapshotter should exist
	cfgDesc, err := image.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cs.Info(ctx, cfgDesc.Digest); err != nil {
		t.Fatal(err)
	}

	dgsts, err := image.RootFS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chainID := identity.ChainID(dgsts)

	if _, err := sn.Stat(ctx, chainID.String()); err != nil {
		t.Fatal(err)
	}

	// step 2: reference snapshotter with lease
	r := leases.Resource{
		ID:   chainID.String(),
		Type: "snapshots/" + snapshotterName,
	}

	if err := ls.AddResource(ctx, l, r); err != nil {
		t.Fatal(err)
	}

	list, err := ls.ListResources(ctx, l)
	if err != nil {
		t.Fatal(err)
	}

	if len(list) != 1 || list[0] != r {
		t.Fatalf("expected (%v), but got (%v)", []leases.Resource{r}, list)
	}

	// step 3: remove image and check the status of snapshotter and content
	if err := imgSrv.Delete(ctx, imageName, images.SynchronousDelete()); err != nil {
		t.Fatal(err)
	}

	// config should be removed but the snapshotter should exist
	if _, err := cs.Info(ctx, cfgDesc.Digest); !errdefs.IsNotFound(err) {
		t.Fatalf("expected error(%v), but got(%v)", errdefs.ErrNotFound, err)
	}

	if _, err := sn.Stat(ctx, chainID.String()); err != nil {
		t.Fatal(err)
	}

	// step 4: remove resource from the lease and check the list API
	if err := ls.DeleteResource(ctx, l, r); err != nil {
		t.Fatal(err)
	}

	list, err = ls.ListResources(ctx, l)
	if err != nil {
		t.Fatal(err)
	}

	if len(list) != 0 {
		t.Fatalf("expected nothing, but got (%v)", list)
	}

	// step 5: remove the lease to check the status of snapshotter
	if err := ls.Delete(ctx, l, leases.SynchronousDelete); err != nil {
		t.Fatal(err)
	}

	if _, err := sn.Stat(ctx, chainID.String()); !errdefs.IsNotFound(err) {
		t.Fatalf("expected error(%v), but got(%v)", errdefs.ErrNotFound, err)
	}
}
