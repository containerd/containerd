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
	"fmt"
	"testing"

	"github.com/containerd/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestDiffCompression(t *testing.T) {
	testCases := []struct {
		mediaType           string
		expectedCompression string
		expectedError       error
	}{
		{
			mediaType:           MediaTypeDockerSchema2Layer,
			expectedCompression: "unknown",
			expectedError:       nil,
		},
		{
			mediaType:           MediaTypeDockerSchema2LayerForeign,
			expectedCompression: "unknown",
			expectedError:       nil,
		},
		{
			mediaType:           MediaTypeDockerSchema2LayerGzip,
			expectedCompression: "gzip",
			expectedError:       nil,
		},
		{
			mediaType:           MediaTypeDockerSchema2LayerForeignGzip,
			expectedCompression: "gzip",
			expectedError:       nil,
		},
		{
			mediaType:           ocispec.MediaTypeImageLayer,
			expectedCompression: "",
			expectedError:       nil,
		},
		{
			mediaType:           ocispec.MediaTypeImageLayerGzip,
			expectedCompression: "gzip",
			expectedError:       nil,
		},
		{
			mediaType:           "application/octet-stream",
			expectedCompression: "",
			expectedError:       fmt.Errorf("unrecognized media type %s: %w", "application/octet-stream", errdefs.ErrNotImplemented),
		}, {
			mediaType:           "test",
			expectedCompression: "",
			expectedError:       fmt.Errorf("unrecognized media type %s: %w", "test", errdefs.ErrNotImplemented),
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("MediaType=%s", tc.mediaType), func(t *testing.T) {
			compression, err := DiffCompression(context.Background(), tc.mediaType)

			if compression != tc.expectedCompression {
				t.Errorf("Expected compression %q but got %q", tc.expectedCompression, compression)
			}

			if err != nil && err.Error() != tc.expectedError.Error() {
				t.Errorf("Expected error %q but got %q", tc.expectedError, err)
			}
		})
	}
}
