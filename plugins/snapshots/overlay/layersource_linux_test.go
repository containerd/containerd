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

package overlay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/snapshots"
)

func TestLayerSourceRoundTrip(t *testing.T) {
	// No labels means the layer directory is the snapshot's own.
	src, err := layerSourceFromInfo(snapshots.Info{Name: "local"})
	require.NoError(t, err)
	assert.False(t, src.populated(), "a local directory is applied into")
	assert.True(t, src.owned(), "a local directory is the snapshot's to write and remove")
	assert.Nil(t, src.labels(), "a local directory does not record a source")

	want := layerSource{Kind: layerSourceCache, Ref: "/cache/snapshots/1/fs"}
	src, err = layerSourceFromInfo(snapshots.Info{Name: "cached", Labels: want.labels()})
	require.NoError(t, err)
	assert.Equal(t, want, src)
	assert.True(t, src.populated())
	assert.False(t, src.owned(), "a directory from another source is shared")

	wantWithUsage := layerSource{
		Kind:  layerSourceCache,
		Ref:   "/cache/snapshots/2/fs",
		Usage: snapshots.Usage{Size: 4096, Inodes: 17},
	}
	src, err = layerSourceFromInfo(snapshots.Info{Name: "cached-usage", Labels: wantWithUsage.labels()})
	require.NoError(t, err)
	assert.Equal(t, wantWithUsage, src)
}

func TestLayerSourceMalformed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
	}{
		{"ref without a source", map[string]string{layerSourceRefLabel: "/cache/snapshots/1/fs"}},
		{"size without a source", map[string]string{layerSourceSizeLabel: "10"}},
		{"source without a ref", map[string]string{layerSourceKindLabel: "cache"}},
		{"relative cache ref", map[string]string{layerSourceKindLabel: "cache", layerSourceRefLabel: "relative/1/fs"}},
		{"negative size", map[string]string{layerSourceKindLabel: "cache", layerSourceRefLabel: "/x", layerSourceSizeLabel: "-1"}},
		{"non-numeric inodes", map[string]string{layerSourceKindLabel: "cache", layerSourceRefLabel: "/x", layerSourceInodesLabel: "abc"}},
		{"unknown source", map[string]string{layerSourceKindLabel: "wormhole", layerSourceRefLabel: "/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A record that cannot be understood must not read as "no record".
			_, err := layerSourceFromInfo(snapshots.Info{Name: "s", Labels: tc.labels})
			assert.Error(t, err)
		})
	}
}

// TestPrivateLabels covers picking out the labels the snapshotter keeps for
// itself. Commit and Update carry them across. A committed snapshot still
// records where its layer directory is.
func TestPrivateLabels(t *testing.T) {
	assert.Nil(t, privateLabels(nil))
	assert.Nil(t, privateLabels(map[string]string{
		"containerd.io/snapshot.ref":              "sha256:abcd",
		"containerd.io/snapshot/diff-id":          "sha256:ef01",
		"containerd.io/snapshot/overlayfs/sneaky": "/etc/shadow",
	}), "only the snapshotter's own namespace is private")

	assert.Equal(t, map[string]string{
		layerSourceKindLabel: "cache",
		layerSourceRefLabel:  "/cache/snapshots/1/fs",
	}, privateLabels(map[string]string{
		"containerd.io/snapshot.ref": "sha256:abcd",
		layerSourceKindLabel:         "cache",
		layerSourceRefLabel:          "/cache/snapshots/1/fs",
	}))
}

// TestLayerSourceLabelIsNotInherited covers the reason the snapshotter's labels
// sit outside the "containerd.io/snapshot/" namespace (see labelPrefix).
func TestLayerSourceLabelIsNotInherited(t *testing.T) {
	annotations := map[string]string{
		layerSourceKindLabel: "cache",
		layerSourceRefLabel:  "/etc/shadow",
	}
	assert.Empty(t, snapshots.FilterInheritedLabels(annotations),
		"an image must not be able to set the snapshotter's own labels")
}

func TestResolveLayerDir(t *testing.T) {
	o := &snapshotter{root: t.TempDir()}

	t.Run("local directory", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(o.upperPath("local"), 0755))

		path, src, err := o.resolveLayerDir("local", snapshots.Info{Name: "local"})
		require.NoError(t, err)
		assert.Equal(t, o.upperPath("local"), path)
		assert.True(t, src.owned())
	})

	// A local layer directory is a directory this snapshotter created. Resolving
	// a symlink would hand back a path outside the snapshot as though the
	// snapshot owned it.
	t.Run("linked directory is refused", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(filepath.Dir(o.upperPath("linked")), 0755))
		target := filepath.Join(t.TempDir(), "elsewhere")
		require.NoError(t, os.MkdirAll(target, 0755))
		require.NoError(t, os.Symlink(target, o.upperPath("linked")))

		_, _, err := o.resolveLayerDir("linked", snapshots.Info{Name: "linked"})
		require.Error(t, err)
		assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	})

	t.Run("recorded directory", func(t *testing.T) {
		cachedDir := filepath.Join(t.TempDir(), "cached-fs")
		require.NoError(t, os.MkdirAll(cachedDir, 0755))

		info := snapshots.Info{Name: "cached", Labels: layerSource{Kind: layerSourceCache, Ref: cachedDir}.labels()}
		path, src, err := o.resolveLayerDir("cached", info)
		require.NoError(t, err)
		assert.Equal(t, cachedDir, path, "the cache entry is used in place")
		assert.False(t, src.owned())
	})

	t.Run("recorded path that is not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "notadir")
		require.NoError(t, os.WriteFile(file, []byte("file"), 0644))

		info := snapshots.Info{Name: "file", Labels: layerSource{Kind: layerSourceCache, Ref: file}.labels()}
		_, _, err := o.resolveLayerDir("file", info)
		require.Error(t, err)
		assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	})

	t.Run("recorded directory that is gone", func(t *testing.T) {
		gone := filepath.Join(t.TempDir(), "pruned-fs")

		info := snapshots.Info{Name: "pruned", Labels: layerSource{Kind: layerSourceCache, Ref: gone}.labels()}
		_, _, err := o.resolveLayerDir("pruned", info)
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}
