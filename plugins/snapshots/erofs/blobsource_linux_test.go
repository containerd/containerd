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

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
)

func TestBlobSourceRoundTrip(t *testing.T) {
	// No labels means the blob is the snapshot's own.
	src, err := blobSourceFromInfo(snapshots.Info{Name: "local"})
	require.NoError(t, err)
	assert.False(t, src.populated(), "a local blob is applied into")
	assert.True(t, src.owned(), "a local blob is the snapshot's to write and remove")
	assert.Nil(t, src.labels(), "a local blob does not record a source")

	want := blobSource{Kind: blobSourceCache, Ref: "/cache/sha256/ab/abcd.erofs"}
	src, err = blobSourceFromInfo(snapshots.Info{Name: "cached", Labels: want.labels()})
	require.NoError(t, err)
	assert.Equal(t, want, src)
	assert.True(t, src.populated())
	assert.False(t, src.owned(), "a blob from another source is shared")
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
			// A record that cannot be understood must not read as "no record"
			// (see errNoLayerBlob).
			_, err := blobSourceFromInfo(snapshots.Info{Name: "s", Labels: tc.labels})
			assert.Error(t, err)
		})
	}
}

// TestPrivateLabels covers picking out the labels the snapshotter keeps for
// itself. Commit and Update carry them across. A committed snapshot still
// records where its blob is.
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
// sit outside the "containerd.io/snapshot/" namespace (see labelPrefix).
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
		require.NoError(t, os.MkdirAll(filepath.Dir(s.layerBlobPath(id)), 0755))
	}

	t.Run("no blob", func(t *testing.T) {
		mkSnapshot(t, "empty")
		_, _, err := s.resolveBlob("empty", snapshots.Info{Name: "empty"})
		assert.ErrorIs(t, err, errNoLayerBlob,
			"an unapplied snapshot must be distinguishable from a broken one")
		assert.ErrorIs(t, err, os.ErrNotExist,
			"a caller that only needs to know the blob is absent still sees that")
	})

	// A local blob is a regular file this snapshotter wrote. Resolving a link
	// would hand back a path outside the snapshot as though the snapshot owned
	// it. Commit would then convert into, or set attributes on, whatever it
	// points at. It must not read as an absent blob either, which is what makes
	// Commit convert one in its place.
	t.Run("linked blob is refused", func(t *testing.T) {
		mkSnapshot(t, "linked")
		target := filepath.Join(t.TempDir(), "elsewhere.erofs")
		require.NoError(t, os.WriteFile(target, []byte("not ours"), 0644))
		require.NoError(t, os.Symlink(target, s.layerBlobPath("linked")))

		_, _, err := s.resolveBlob("linked", snapshots.Info{Name: "linked"})
		require.Error(t, err)
		assert.NotErrorIs(t, err, errNoLayerBlob, "a link must not pass as an unapplied layer")
		assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
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
		assert.Equal(t, blob, path, "the cache entry is used in place")
		assert.False(t, src.owned())
	})

	// A ref that resolves to something other than a file is not a layer. It
	// must not pass as complete: Commit would measure it and record the
	// snapshot, and the mount would fail later.
	t.Run("recorded blob that is not a file", func(t *testing.T) {
		mkSnapshot(t, "dir")
		blobDir := filepath.Join(t.TempDir(), "notablob.erofs")
		require.NoError(t, os.MkdirAll(blobDir, 0755))

		info := snapshots.Info{Name: "dir", Labels: blobSource{Kind: blobSourceCache, Ref: blobDir}.labels()}
		_, _, err := s.resolveBlob("dir", info)
		require.Error(t, err)
		assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
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

// TestFsverityOnlyForAnOwnedBlob covers which blob the fsverity check applies
// to. Commit only enables fsverity on a blob the snapshot owns. Only such a
// blob can be measured. Measuring a cache entry would require the operator to
// have enabled fsverity on content this snapshotter never wrote.
// NewSnapshotter rejects the two together; this is a guard, not a reachable
// configuration.
func TestFsverityOnlyForAnOwnedBlob(t *testing.T) {
	s := &snapshotter{root: t.TempDir(), enableFsverity: true}
	snap := storage.Snapshot{ID: "1", Kind: snapshots.KindActive}
	require.NoError(t, os.MkdirAll(filepath.Dir(s.layerBlobPath(snap.ID)), 0755))

	cached := filepath.Join(t.TempDir(), "cached.erofs")
	require.NoError(t, os.WriteFile(cached, []byte("layer"), 0644))

	info := snapshots.Info{Name: "cached", Labels: blobSource{Kind: blobSourceCache, Ref: cached}.labels()}
	mounts, err := s.mounts(snap, info, nil)
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	assert.Equal(t, cached, mounts[0].Source, "the cache entry is mounted unmeasured")

	// A blob the snapshot does own is measured. A plain file fails.
	require.NoError(t, os.WriteFile(s.layerBlobPath(snap.ID), []byte("layer"), 0644))
	_, err = s.mounts(snap, snapshots.Info{Name: "local"}, nil)
	require.Error(t, err, "a local blob must still be measured")
	assert.Contains(t, err.Error(), "fsverity")
}
