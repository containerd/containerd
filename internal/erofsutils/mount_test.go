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

// TestMountsToLayerStagedBlob covers that resolving the layer directory is
// unaffected by a staged blob: Compare only reads from it, so refusing belongs
// on the apply path, not here.
func TestMountsToLayerStagedBlob(t *testing.T) {
	layer := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(layer, ".erofslayer"), nil, 0644))
	cached := filepath.Join(t.TempDir(), "cached.erofs")
	require.NoError(t, os.WriteFile(cached, []byte("shared blob"), 0644))
	require.NoError(t, os.Symlink(cached, filepath.Join(layer, "layer.erofs")))

	got, err := MountsToLayer([]mount.Mount{{
		Type:    "erofs",
		Source:  filepath.Join(layer, "layer.erofs"),
		Options: []string{"ro"},
	}})
	require.NoError(t, err)
	assert.Equal(t, layer, got)
}
