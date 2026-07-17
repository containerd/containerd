//go:build linux

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

package podsandbox

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func TestShutdownRemovesUnreportedRestoreImages(t *testing.T) {
	ctx := namespaces.WithNamespace(t.Context(), "k8s.io")
	bdb, err := bolt.Open(filepath.Join(t.TempDir(), "metadata.db"), 0o600, nil)
	require.NoError(t, err)
	db := metadata.NewDB(bdb, nil, nil)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	require.NoError(t, db.Init(ctx))
	imageStore := &restoreCleanupImageStore{Store: metadata.NewImageStore(db)}
	c, err := client.New("", client.WithServices(client.WithImageStore(imageStore)))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, c.Close()) })
	for _, image := range []struct{ name, owner string }{
		{"unreported-app", "restored"},
		{"unreported-sidecar", "restored"},
		{"other-restore", "other"},
		{"ordinary-image", ""},
	} {
		_, err := imageStore.Store.Create(ctx, images.Image{
			Name:   image.name,
			Target: imagespec.Descriptor{MediaType: imagespec.MediaTypeImageIndex, Digest: digest.FromString(image.name), Size: 1},
			Labels: map[string]string{restoreSandboxLabel: image.owner},
		})
		require.NoError(t, err)
	}
	controller := &Controller{store: NewStore(), checkpoint: &CheckpointService{client: c}}
	imageStore.fail = true
	require.ErrorIs(t, controller.Shutdown(ctx, "restored"), errdefs.ErrUnavailable)
	imageStore.fail = false
	// No controller store entry exists, as can happen during restart recovery.
	require.NoError(t, controller.Shutdown(ctx, "restored"))
	require.NoError(t, controller.Shutdown(ctx, "restored"))
	remaining, err := imageStore.List(ctx)
	require.NoError(t, err)
	var names []string
	for _, image := range remaining {
		names = append(names, image.Name)
	}
	assert.ElementsMatch(t, []string{"other-restore", "ordinary-image"}, names)
}

type restoreCleanupImageStore struct {
	images.Store
	fail bool
}

func (s *restoreCleanupImageStore) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	if s.fail {
		return errdefs.ErrUnavailable
	}
	return s.Store.Delete(ctx, name, opts...)
}
