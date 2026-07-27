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
	"context"
	"encoding/json"
	"testing"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeImage implements the subset of Image used by WithImageConfigLabels:
// Config returns a descriptor with the config blob inlined in Data, so the
// content store is never consulted.
type fakeImage struct {
	Image
	config ocispec.Descriptor
}

func (i fakeImage) Config(context.Context) (ocispec.Descriptor, error) {
	return i.config, nil
}

func (i fakeImage) ContentStore() content.Store {
	return nil
}

func TestWithImageConfigLabels(t *testing.T) {
	blob, err := json.Marshal(ocispec.Image{
		Config: ocispec.ImageConfig{
			Labels: map[string]string{
				"foo":                          "bar",
				"containerd.io/restart.policy": "always",
				"io.cri-containerd.kind":       "sandbox",
			},
		},
	})
	require.NoError(t, err)

	img := fakeImage{
		config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes(blob),
			Size:      int64(len(blob)),
			Data:      blob,
		},
	}

	var c containers.Container
	require.NoError(t, WithImageConfigLabels(img)(t.Context(), nil, &c))

	// labels in the namespaces reserved for containerd and the CRI plugin
	// are not copied from the image config
	assert.Equal(t, map[string]string{"foo": "bar"}, c.Labels)
}
