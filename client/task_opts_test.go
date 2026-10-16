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

package client

import (
	"testing"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestTaskCheckpointDescriptor(t *testing.T) {
	valid := ocispec.Descriptor{
		MediaType: images.MediaTypeContainerd1Checkpoint,
		Digest:    digest.FromString("checkpoint"),
		Size:      42,
	}

	tests := map[string]struct {
		manifests []ocispec.Descriptor
		wantErr   string
	}{
		"valid": {manifests: []ocispec.Descriptor{valid}},
		"missing": {
			manifests: []ocispec.Descriptor{{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("layer"), Size: 1}},
			wantErr:   "checkpoint not found",
		},
		"duplicate": {
			manifests: []ocispec.Descriptor{valid, valid},
			wantErr:   "multiple task checkpoints",
		},
		"host path annotation": {
			manifests: []ocispec.Descriptor{{
				MediaType:   valid.MediaType,
				Digest:      valid.Digest,
				Size:        valid.Size,
				Annotations: map[string]string{"RestoreFromPath": "/host/path"},
			}},
			wantErr: "forbidden annotations",
		},
		"zero size": {
			manifests: []ocispec.Descriptor{{MediaType: valid.MediaType, Digest: valid.Digest}},
			wantErr:   "invalid size",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			desc, err := taskCheckpointDescriptor(&ocispec.Index{Manifests: test.manifests})
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, valid, *desc)
		})
	}
}
