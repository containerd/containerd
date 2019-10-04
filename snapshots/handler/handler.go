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

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	TargetSnapshotLabel = "containerd.io/snapshot.ref"
	TargetRefLabel      = "containerd.io/snapshot/target.reference"
	TargetDigestLabel   = "containerd.io/snapshot/target.digest"
)

// FilterLayerBySnapshotter filters out layers from download candidates if the
// snapshotter can prepare the layer snapshot without pulling it.
func FilterLayerBySnapshotter(f images.HandlerFunc, sn snapshots.Snapshotter, store content.Store, fetcher remotes.Fetcher, ref string) images.HandlerFunc {
	return func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		children, err := f(ctx, desc)
		if err != nil {
			return nil, err
		}

		if desc.MediaType == ocispec.MediaTypeImageManifest ||
			desc.MediaType == images.MediaTypeDockerSchema2Manifest {
			p, err := content.ReadBlob(ctx, store, desc)
			if err != nil {
				return nil, err
			}
			var manifest ocispec.Manifest
			if err := json.Unmarshal(p, &manifest); err != nil {
				return nil, err
			}

			if _, err := remotes.FetchHandler(store, fetcher)(ctx, manifest.Config); err != nil {
				return nil, err
			}
			chain, err := images.RootFS(ctx, store, manifest.Config)
			if err != nil {
				return nil, err
			}

			// Get the index of a layer which is the lowest non-skippable layer. We avoid
			// downloading skippable layers which are lower than it by filtering out them.
			i := firstFalseIdx(manifest.Layers, chain, isPullSkippable(ctx, sn, ref))
			children = exclude(children, manifest.Layers[:i])
		}

		return children, nil
	}
}

func isPullSkippable(ctx context.Context, sn snapshots.Snapshotter, ref string) func(ocispec.Descriptor, []digest.Digest) bool {

	// Check if the given layer is a pull-skippable snapshot. Using chain, this
	// function generates chainID to prepare/commit the snapshot so that the
	// snapshot can be Prepare()ed later using the chainID as a normal way.
	return func(layer ocispec.Descriptor, chain []digest.Digest) bool {
		var (
			parent      = identity.ChainID(chain[:len(chain)-1]).String()
			chainID     = identity.ChainID(chain).String()
			layerDigest = layer.Digest.String()
		)

		// If this layer can be provided without pulling the actual contents, the
		// snapshotter gives us ErrAlreadyExists.
		for {
			pKey := getUniqueKey(ctx, sn, chainID)
			if _, err := sn.Prepare(ctx, pKey, parent, snapshots.WithLabels(map[string]string{
				TargetSnapshotLabel: chainID,
				TargetRefLabel:      ref,
				TargetDigestLabel:   layerDigest,
			})); !errdefs.IsAlreadyExists(err) {
				sn.Remove(ctx, pKey)
				return false
			}
			if _, err := sn.Stat(ctx, pKey); err == nil {

				// We got ErrAlreadyExist and the ActiveSnapshot already exists. This
				// doesn't indicade pull-skippable snapshot existence but key confliction.
				// Try again with another key.
				continue
			}

			break
		}

		return true
	}
}

func getUniqueKey(ctx context.Context, sn snapshots.Snapshotter, chainID string) (key string) {
	for {
		t := time.Now()
		var b [3]byte
		// Ignore read failures, just decreases uniqueness
		rand.Read(b[:])
		uniquePart := fmt.Sprintf("%d-%s", t.Nanosecond(), base64.URLEncoding.EncodeToString(b[:]))
		key = fmt.Sprintf("target-%s %s", uniquePart, chainID)
		if _, err := sn.Stat(ctx, key); err == nil {
			continue
		}
		break
	}
	return
}

func firstFalseIdx(layers []ocispec.Descriptor, chain []digest.Digest, f func(ocispec.Descriptor, []digest.Digest) bool) int {
	for i, l := range layers {
		if !f(l, chain[:i+1]) {
			return i
		}
	}
	return len(layers)
}

func exclude(a []ocispec.Descriptor, b []ocispec.Descriptor) (res []ocispec.Descriptor) {
	bmap := make(map[string]ocispec.Descriptor)
	for _, vb := range b {
		bmap[vb.Digest.String()] = vb
	}
	for _, va := range a {
		if _, ok := bmap[va.Digest.String()]; !ok {
			res = append(res, va)
		}
	}
	return
}
