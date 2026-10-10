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

package images

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

func TestRepoDigestRef(t *testing.T) {
	desc := testDesc("image")
	dgst := desc.Digest.String()

	for _, tc := range []struct {
		name     string
		ref      string
		expected []string
	}{
		{
			name:     "Tag",
			ref:      "registry.test/app:latest",
			expected: []string{"registry.test/app:latest", "registry.test/app@" + dgst},
		},
		{
			name:     "RegistryPort",
			ref:      "localhost:5000/app:v1",
			expected: []string{"localhost:5000/app:v1", "localhost:5000/app@" + dgst},
		},
		{
			name:     "Digest",
			ref:      "registry.test/app@" + dgst,
			expected: []string{"registry.test/app@" + dgst},
		},
		{
			name:     "TagAndDigest",
			ref:      "registry.test/app:latest@" + dgst,
			expected: []string{"registry.test/app:latest@" + dgst, "registry.test/app@" + dgst},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db := newTestDB(t)
			store := metadata.NewImageStore(db)

			testPull(ctx, t, store, tc.ref, desc)

			expected := map[string]digest.Digest{}
			for _, name := range tc.expected {
				expected[name] = desc.Digest
			}
			assertImages(ctx, t, store, expected)
		})
	}
}

func TestRepoDigestRefTagUpdate(t *testing.T) {
	const (
		ref  = "registry.test/app:latest"
		repo = "registry.test/app"
	)
	descA := testDesc("A")
	descB := testDesc("B")

	ctx, db := newTestDB(t)
	store := metadata.NewImageStore(db)

	testPull(ctx, t, store, ref, descA)
	assertImages(ctx, t, store, map[string]digest.Digest{
		ref:                                descA.Digest,
		repo + "@" + descA.Digest.String(): descA.Digest,
	})

	// latest now points to B
	testPull(ctx, t, store, ref, descB)
	expected := map[string]digest.Digest{
		ref:                                descB.Digest,
		repo + "@" + descA.Digest.String(): descA.Digest,
		repo + "@" + descB.Digest.String(): descB.Digest,
	}
	assertImages(ctx, t, store, expected)

	_, err := db.GarbageCollect(ctx)
	require.NoError(t, err)
	assertImages(ctx, t, store, expected)

	// digest references are kept after the tag is removed
	require.NoError(t, store.Delete(ctx, ref))
	delete(expected, ref)
	_, err = db.GarbageCollect(ctx)
	require.NoError(t, err)
	assertImages(ctx, t, store, expected)
}

func TestRepoDigestRefExisting(t *testing.T) {
	desc := testDesc("A")
	existing := images.Image{
		Name:   "registry.test/app@" + desc.Digest.String(),
		Target: desc,
		Labels: map[string]string{"existing": "label"},
	}

	ctx, db := newTestDB(t)
	store := metadata.NewImageStore(db)

	_, err := store.Create(ctx, existing)
	require.NoError(t, err)

	testPull(ctx, t, store, "registry.test/app:latest", desc)

	img, err := store.Get(ctx, existing.Name)
	require.NoError(t, err)
	assert.Equal(t, existing.Labels, img.Labels)
}

// testPull mimics the image store updates done by ctr pull
func testPull(ctx context.Context, t *testing.T, store images.Store, ref string, desc ocispec.Descriptor) {
	t.Helper()

	img := images.Image{Name: ref, Target: desc}
	created, err := store.Create(ctx, img)
	if errdefs.IsAlreadyExists(err) {
		created, err = store.Update(ctx, img)
	}
	require.NoError(t, err)
	require.NoError(t, createRepoDigestRef(ctx, store, created))
}

func newTestDB(t *testing.T) (context.Context, *metadata.DB) {
	ctx := namespaces.WithNamespace(t.Context(), "testing")
	dir := t.TempDir()

	cs, err := local.NewStore(filepath.Join(dir, "content"))
	require.NoError(t, err)

	bdb, err := bolt.Open(filepath.Join(dir, "metadata.db"), 0644, nil)
	require.NoError(t, err)

	db := metadata.NewDB(bdb, cs, nil)
	require.NoError(t, db.Init(ctx))
	t.Cleanup(func() {
		assert.NoError(t, db.Close())
	})

	return ctx, db
}

func testDesc(s string) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromString(s),
		Size:      int64(len(s)),
	}
}

func assertImages(ctx context.Context, t *testing.T, store images.Store, expected map[string]digest.Digest) {
	t.Helper()

	imgs, err := store.List(ctx)
	require.NoError(t, err)

	actual := make(map[string]digest.Digest, len(imgs))
	for _, img := range imgs {
		actual[img.Name] = img.Target.Digest
	}
	assert.Equal(t, expected, actual)
}
