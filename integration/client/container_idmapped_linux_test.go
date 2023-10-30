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
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/snapshots/overlay/overlayutils"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestIDMappedOverlay(t *testing.T) {
	var (
		upperPath   string
		lowerPaths  []string
		snapshotter = "overlayfs"
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	if ok, err := overlayutils.SupportsIDMappedMounts(); err != nil || !ok {
		t.Skip("overlayfs doesn't support idmapped mounts")
	}

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	image, err := client.Pull(ctx, testMultiLayeredImage, containerd.WithPullUnpack)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image %s pulled!", testMultiLayeredImage)

	hostID := uint32(33)
	contID := uint32(0)
	length := uint32(65536)

	uidMap := specs.LinuxIDMapping{
		ContainerID: contID,
		HostID:      hostID,
		Size:        length,
	}
	gidMap := specs.LinuxIDMapping{
		ContainerID: contID,
		HostID:      hostID,
		Size:        length,
	}

	container, err := client.NewContainer(ctx, id,
		containerd.WithImage(image),
		containerd.WithImageConfigLabels(image),
		containerd.WithSnapshotter(snapshotter),
		containerd.WithNewSnapshot(id, image, containerd.WithRemapperLabels(uidMap.ContainerID, uidMap.HostID, gidMap.ContainerID, gidMap.HostID, length)),
		containerd.WithNewSpec(oci.WithImageConfig(image),
			oci.WithUserNamespace([]specs.LinuxIDMapping{uidMap}, []specs.LinuxIDMapping{gidMap}),
			longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, containerd.WithSnapshotCleanup)

	t.Logf("container %s created!", id)
	o := client.SnapshotService(snapshotter)
	mounts, err := o.Mounts(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	m := mounts[0]
	if m.Type != "overlay" {
		t.Fatalf("invalid mount -- %s; expected %s", m.Type, snapshotter)
	}

	for _, o := range m.Options {
		if strings.HasPrefix(o, "upperdir=") {
			upperPath = strings.TrimPrefix(o, "upperdir=")
		} else if strings.HasPrefix(o, "lowerdir=") {
			lowerPaths = strings.Split(strings.TrimPrefix(o, "lowerdir="), ",")
		}
	}

	t.Log("check lowerdirs")
	for _, l := range lowerPaths {
		if _, err := os.Stat(l); err == nil {
			t.Fatalf("lowerdir=%s should not exist", l)
		}
	}

	t.Logf("check stats of uppedir=%s", upperPath)
	st, err := os.Stat(upperPath)
	if err != nil {
		t.Fatalf("failed to stat %s", upperPath)
	}

	if stat, ok := st.Sys().(*syscall.Stat_t); !ok {
		t.Fatalf("incompatible types after stat call: *syscall.Stat_t expected")
	} else if stat.Uid != uidMap.HostID || stat.Gid != gidMap.HostID {
		t.Fatalf("bad mapping: expected {uid: %d, gid: %d}; real {uid: %d, gid: %d}", uidMap.HostID, gidMap.HostID, int(stat.Uid), int(stat.Gid))
	}
}
