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

package erofs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/snapshots"
)

func TestBlobSourceRoundTrip(t *testing.T) {
	// No labels means the blob is the snapshot's own.
	src, err := blobSourceFromInfo(snapshots.Info{Name: "local"})
	require.NoError(t, err)
	assert.False(t, src.populated(), "a local blob is applied into, not already there")
	assert.True(t, src.owned(), "a local blob is the snapshot's to write and remove")
	assert.Nil(t, src.labels(), "a local blob records nothing, since its absence says so")

	want := blobSource{Kind: blobSourceCache, Ref: "/cache/sha256/ab/abcd.erofs"}
	src, err = blobSourceFromInfo(snapshots.Info{Name: "cached", Labels: want.labels()})
	require.NoError(t, err)
	assert.Equal(t, want, src)
	assert.True(t, src.populated())
	assert.False(t, src.owned(), "a blob from elsewhere is shared and not ours to touch")
}

func TestBlobSourceMalformed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
	}{
		{"ref without a source", map[string]string{blobSourceRefLabel: "/cache/blob.erofs"}},
		{"source without a ref", map[string]string{blobSourceKindLabel: "cache"}},
		{"unknown source", map[string]string{blobSourceKindLabel: "wormhole", blobSourceRefLabel: "/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A record that cannot be understood must not read as "no record":
			// that would say the layer was never applied and invite a caller to
			// convert or write one.
			_, err := blobSourceFromInfo(snapshots.Info{Name: "s", Labels: tc.labels})
			assert.Error(t, err)
		})
	}
}

// TestPrivateLabels covers picking out the labels the snapshotter keeps for
// itself, which Commit and Update have to carry across so a committed snapshot
// does not lose track of its blob.
func TestPrivateLabels(t *testing.T) {
	assert.Nil(t, privateLabels(nil))
	assert.Nil(t, privateLabels(map[string]string{
		"containerd.io/snapshot.ref":          "sha256:abcd",
		"containerd.io/snapshot/diff-id":      "sha256:ef01",
		"containerd.io/snapshot/erofs/sneaky": "/etc/shadow",
	}), "only the snapshotter's own namespace is private")

	assert.Equal(t, map[string]string{
		blobSourceKindLabel: "cache",
		blobSourceRefLabel:  "/cache/blob.erofs",
	}, privateLabels(map[string]string{
		"containerd.io/snapshot.ref": "sha256:abcd",
		blobSourceKindLabel:          "cache",
		blobSourceRefLabel:           "/cache/blob.erofs",
	}))
}

// TestBlobSourceLabelIsNotInherited covers the reason the snapshotter's labels
// sit outside the "containerd.io/snapshot/" namespace: labels in that namespace
// are inherited from image annotations, so a manifest could otherwise claim its
// layer lives at any path on the host.
func TestBlobSourceLabelIsNotInherited(t *testing.T) {
	annotations := map[string]string{
		blobSourceKindLabel: "cache",
		blobSourceRefLabel:  "/etc/shadow",
	}
	assert.Empty(t, snapshots.FilterInheritedLabels(annotations),
		"an image must not be able to set the snapshotter's own labels")
}

func TestResolveBlob(t *testing.T) {
	s := &snapshotter{root: t.TempDir()}
	mkSnapshot := func(t *testing.T, id string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(s.snapshotDir(id), 0755))
	}

	t.Run("no blob", func(t *testing.T) {
		mkSnapshot(t, "empty")
		_, _, err := s.resolveBlob("empty", snapshots.Info{Name: "empty"})
		assert.ErrorIs(t, err, errNoLayerBlob,
			"an unapplied snapshot must be distinguishable from a broken one")
	})

	t.Run("local blob", func(t *testing.T) {
		mkSnapshot(t, "local")
		require.NoError(t, os.WriteFile(s.layerBlobPath("local"), []byte("layer"), 0644))

		path, src, err := s.resolveBlob("local", snapshots.Info{Name: "local"})
		require.NoError(t, err)
		assert.Equal(t, s.layerBlobPath("local"), path)
		assert.True(t, src.owned())
	})

	t.Run("recorded blob", func(t *testing.T) {
		mkSnapshot(t, "cached")
		blob := filepath.Join(t.TempDir(), "cached.erofs")
		require.NoError(t, os.WriteFile(blob, []byte("layer"), 0644))

		info := snapshots.Info{Name: "cached", Labels: blobSource{Kind: blobSourceCache, Ref: blob}.labels()}
		path, src, err := s.resolveBlob("cached", info)
		require.NoError(t, err)
		assert.Equal(t, blob, path, "the blob is used where it lies")
		assert.False(t, src.owned())
	})

	t.Run("recorded blob that is gone", func(t *testing.T) {
		mkSnapshot(t, "pruned")
		blob := filepath.Join(t.TempDir(), "pruned.erofs")

		info := snapshots.Info{Name: "pruned", Labels: blobSource{Kind: blobSourceCache, Ref: blob}.labels()}
		_, _, err := s.resolveBlob("pruned", info)
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.NotErrorIs(t, err, errNoLayerBlob,
			"a blob that was recorded and then pruned is broken, not unapplied")
	})
}
