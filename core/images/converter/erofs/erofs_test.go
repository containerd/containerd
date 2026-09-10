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
	"context"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/images"
)

// TestLayerConvertFuncSkipsChunkIndex verifies that a standalone chunk-index
// layer (see the EROFS image layer format specification,
// https://github.com/erofs/erofs-image-spec, §2.2) is left untouched by the
// converter rather than being fed to mkfs.erofs, which is not meaningful
// for a layer that is not itself a filesystem image.
func TestLayerConvertFuncSkipsChunkIndex(t *testing.T) {
	convertFn := LayerConvertFunc()

	desc := ocispec.Descriptor{
		MediaType: images.MediaTypeErofsChunkIndex,
		Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000",
		Size:      1,
	}

	// cs is never touched: the function must return before doing any
	// content store I/O for a skippable layer type.
	newDesc, err := convertFn(context.Background(), nil, desc)
	assert.NoError(t, err)
	assert.Nil(t, newDesc, "a skippable layer must not be converted")
}
