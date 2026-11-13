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
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
)

// TestBuildCacheSkipsChunkIndex verifies that a standalone chunk-index layer
// (see the EROFS image layer format specification,
// https://github.com/erofs/erofs-image-spec, §2.2) gets no cache entry, since
// it is not itself a filesystem image.
func TestBuildCacheSkipsChunkIndex(t *testing.T) {
	ctx := context.Background()
	tc := imagetest.NewContentStore(ctx, t)

	layer := tc.Blob(images.MediaTypeErofsChunkIndex, []byte("chunk-index-content")).Descriptor
	config := tc.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{
		Platform: platforms.DefaultSpec(),
		RootFS:   ocispec.RootFS{Type: "layers"},
	}).Descriptor
	manifest := tc.Manifest(imagetest.Content{Descriptor: config}, imagetest.Content{Descriptor: layer}).Descriptor

	cacheDir := filepath.Join(t.TempDir(), "cache")
	if err := buildCache(ctx, tc.Store, manifest, platforms.All, cacheDir, false); err != nil {
		t.Fatalf("buildCache: %v", err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Errorf("buildCache created %s for a chunk-index layer, want no cache entries (stat error: %v)", cacheDir, err)
	}
}
