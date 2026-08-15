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
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/images"
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

func TestIsErofsMediaType(t *testing.T) {
	cases := []struct {
		mt   string
		want bool
	}{
		{images.MediaTypeErofsLayer, true},
		{images.MediaTypeErofsLayer + "+zstd", true},
		{images.MediaTypeErofs, true},
		{images.MediaTypeErofs + "+zstd", true},
		// A standalone chunk-index layer is not itself a mountable EROFS
		// filesystem image and must not be treated as one.
		{images.MediaTypeErofsChunkIndex, false},
		{"application/vnd.oci.image.layer.v1.tar+gzip", false},
		{"", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, IsErofsMediaType(c.mt), c.mt)
	}
}
