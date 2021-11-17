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
			if err != nil {
				t.Fatal("failed to marshal manifest", err)
			}

			err = validateMediaType(b, tc.mt)
			if tc.index {
				if err == nil {
					t.Error("manifest should not be a valid index")
				}
			} else {
				if err != nil {
					t.Error("manifest should be valid")
				}
			}
		})
		t.Run("index-"+tc.mt, func(t *testing.T) {
			index := ocispec.Index{
				Manifests: []ocispec.Descriptor{{Size: 1}},
			}
			b, err := json.Marshal(index)
			if err != nil {
				t.Fatal("failed to marshal index", err)
			}

			err = validateMediaType(b, tc.mt)
			if tc.index {
				if err != nil {
					t.Error("index should be valid")
				}
			} else {
				if err == nil {
					t.Error("index should not be a valid manifest")
				}
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
				if err != nil {
					t.Fatal("failed to marshal document", err)
				}

				err = validateMediaType(b, tc.mt)
				if err != nil {
					t.Error("document should be valid", err)
				}
			})
		}
		for _, iv := range tc.invalid {
			t.Run("invalid-"+tc.mt+"-"+iv, func(t *testing.T) {
				doc := struct {
					MediaType string `json:"mediaType"`
				}{MediaType: iv}
				b, err := json.Marshal(doc)
				if err != nil {
					t.Fatal("failed to marshal document", err)
				}

				err = validateMediaType(b, tc.mt)
				if err == nil {
					t.Error("document should not be valid")
				}
			})
		}
	}
	t.Run("schema1", func(t *testing.T) {
		doc := struct {
			FSLayers []string `json:"fsLayers"`
		}{FSLayers: []string{"1"}}
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal("failed to marshal document", err)
		}

		err = validateMediaType(b, "")
		if err == nil {
			t.Error("document should not be valid")
		}

	})
}
