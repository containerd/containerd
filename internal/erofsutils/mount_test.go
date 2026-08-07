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

package erofsutils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/mount"
)

func TestCacheBlobPath(t *testing.T) {
	diffID := digest.FromString("hello")
	enc := diffID.Encoded()

	got := CacheBlobPath("/cache", diffID)

	// <dir>/<algo>/<xx>/<hex>.erofs, where <xx> is the first two hex characters.
	want := filepath.Join("/cache", "sha256", enc[:2], enc+".erofs")
	assert.Equal(t, want, got)
	assert.Equal(t, enc[:2], filepath.Base(filepath.Dir(got)), "blob must live under a 2-char prefix dir")
}

// TestMountsToLayerStagedBlob covers a snapshot whose layer blob was staged from
// the layer content cache: it is a symlink shared with every other snapshot of
// that layer, so it must be refused rather than written through. The refusal is
// not ErrNotImplemented, since no other differ may write this layer either.
func TestMountsToLayerStagedBlob(t *testing.T) {
	layer := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(layer, ".erofslayer"), nil, 0644))
	mounts := []mount.Mount{{
		Type:    "erofs",
		Source:  filepath.Join(layer, "layer.erofs"),
		Options: []string{"ro"},
	}}

	// A regular layer blob (written by the erofs differ) is fine to write to.
	require.NoError(t, os.WriteFile(filepath.Join(layer, "layer.erofs"), nil, 0644))
	got, err := MountsToLayer(mounts)
	require.NoError(t, err)
	assert.Equal(t, layer, got)

	// A staged one is not.
	cached := filepath.Join(t.TempDir(), "cached.erofs")
	require.NoError(t, os.WriteFile(cached, []byte("shared blob"), 0644))
	require.NoError(t, os.Remove(filepath.Join(layer, "layer.erofs")))
	require.NoError(t, os.Symlink(cached, filepath.Join(layer, "layer.erofs")))

	_, err = MountsToLayer(mounts)
	require.Error(t, err)
	// FailedPrecondition, not NotImplemented: no other differ may write this
	// layer either, so the diff service must not fall back to one.
	assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	assert.NotErrorIs(t, err, errdefs.ErrNotImplemented, "a staged layer must not fall back to another differ")

	// The shared blob is untouched.
	data, err := os.ReadFile(cached)
	require.NoError(t, err)
	assert.Equal(t, "shared blob", string(data))
}
