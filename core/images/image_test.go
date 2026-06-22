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
	"encoding/json"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateMediaType(t *testing.T) {
	docTests := []struct {
		mt    string
		index bool
	}{
		{MediaTypeDockerSchema2Manifest, false},
		{ocispec.MediaTypeImageManifest, false},
		{MediaTypeDockerSchema2ManifestList, true},
		{ocispec.MediaTypeImageIndex, true},
	}
	for _, tc := range docTests {
		t.Run("manifest-"+tc.mt, func(t *testing.T) {
			manifest := ocispec.Manifest{
				Config: ocispec.Descriptor{Size: 1},
				Layers: []ocispec.Descriptor{{Size: 2}},
			}
			b, err := json.Marshal(manifest)
			require.NoError(t, err, "failed to marshal manifest")

			err = validateMediaType(b, tc.mt)
			if tc.index {
				assert.Error(t, err, "manifest should not be a valid index")
			} else {
				assert.NoError(t, err, "manifest should be valid")
			}
		})
		t.Run("index-"+tc.mt, func(t *testing.T) {
			index := ocispec.Index{
				Manifests: []ocispec.Descriptor{{Size: 1}},
			}
			b, err := json.Marshal(index)
			require.NoError(t, err, "failed to marshal index")

			err = validateMediaType(b, tc.mt)
			if tc.index {
				assert.NoError(t, err, "index should be valid")
			} else {
				assert.Error(t, err, "index should not be a valid manifest")
			}
		})
	}

	mtTests := []struct {
		mt      string
		valid   []string
		invalid []string
	}{{
		MediaTypeDockerSchema2Manifest,
		[]string{MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest},
		[]string{MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex},
	}, {
		ocispec.MediaTypeImageManifest,
		[]string{MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest},
		[]string{MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex},
	}, {
		MediaTypeDockerSchema2ManifestList,
		[]string{MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex},
		[]string{MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest},
	}, {
		ocispec.MediaTypeImageIndex,
		[]string{MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex},
		[]string{MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest},
	}}
	for _, tc := range mtTests {
		for _, v := range tc.valid {
			t.Run("valid-"+tc.mt+"-"+v, func(t *testing.T) {
				doc := struct {
					MediaType string `json:"mediaType"`
				}{MediaType: v}
				b, err := json.Marshal(doc)
				require.NoError(t, err, "failed to marshal document")

				err = validateMediaType(b, tc.mt)
				assert.NoError(t, err, "document should be valid")
			})
		}
		for _, iv := range tc.invalid {
			t.Run("invalid-"+tc.mt+"-"+iv, func(t *testing.T) {
				doc := struct {
					MediaType string `json:"mediaType"`
				}{MediaType: iv}
				b, err := json.Marshal(doc)
				require.NoError(t, err, "failed to marshal document")

				err = validateMediaType(b, tc.mt)
				assert.Error(t, err, "document should not be valid")
			})
		}
	}
	t.Run("schema1", func(t *testing.T) {
		doc := struct {
			FSLayers []string `json:"fsLayers"`
		}{FSLayers: []string{"1"}}
		b, err := json.Marshal(doc)
		require.NoError(t, err, "failed to marshal document")

		err = validateMediaType(b, "")
		assert.Error(t, err, "document should not be valid")
	})
}
