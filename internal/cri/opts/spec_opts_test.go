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

package opts

import (
	"context"
	"sort"
	"testing"

	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
)

func TestOrderedMounts(t *testing.T) {
	mounts := []*runtime.Mount{
		{ContainerPath: "/a/b/c"},
		{ContainerPath: "/a/b"},
		{ContainerPath: "/a/b/c/d"},
		{ContainerPath: "/a"},
		{ContainerPath: "/b"},
		{ContainerPath: "/b/c"},
	}
	expected := []*runtime.Mount{
		{ContainerPath: "/a"},
		{ContainerPath: "/b"},
		{ContainerPath: "/a/b"},
		{ContainerPath: "/b/c"},
		{ContainerPath: "/a/b/c"},
		{ContainerPath: "/a/b/c/d"},
	}
	sort.Stable(orderedMounts(mounts))
	assert.Equal(t, expected, mounts)
}

func TestImageForConfigVariants(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name     string
		platform string
		label    string
		missing  []int
		same     bool
		target   int
		want     int
	}{
		{name: "client preference", platform: "linux/arm/v7", target: -1, want: 1},
		{name: "label preference", platform: "linux/arm/v6", label: "linux/arm/v7", target: -1, want: 1},
		{name: "missing preferred layer", platform: "linux/arm/v7", missing: []int{1}, target: -1, want: 0},
		{name: "all layers missing", platform: "linux/arm/v7", missing: []int{0, 1}, target: -1, want: 1},
		{name: "selected target layer missing", platform: "linux/amd64", missing: []int{1}, target: 1, want: 1},
		{name: "same platform ordering", platform: "linux/arm/v7", same: true, target: -1, want: 0},
		{name: "same platform missing first layer", platform: "linux/arm/v7", missing: []int{0}, same: true, target: -1, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			cs := imagetest.NewContentStore(ctx, t)
			platform, err := platforms.Parse(test.platform)
			require.NoError(t, err)
			client, err := containerd.New("", containerd.WithServices(containerd.WithContentStore(cs.Store)),
				containerd.WithDefaultPlatform(platforms.Only(platform)))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, client.Close()) })
			config := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{
				Platform: ocispec.Platform{OS: "linux", Architecture: "arm"},
				RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{digest.FromString("shared layer")}},
			})
			var manifests []imagetest.Content
			var layers []imagetest.Content
			for _, variant := range []string{"v6", "v7"} {
				layer := cs.Blob(ocispec.MediaTypeImageLayer, []byte(variant))
				layers = append(layers, layer)
				manifests = append(manifests, imagetest.AddPlatform(cs.Manifest(config, layer),
					ocispec.Platform{OS: "linux", Architecture: "arm", Variant: variant}))
			}
			if test.same {
				manifests[0].Descriptor.Platform = manifests[1].Descriptor.Platform
			}
			for _, n := range test.missing {
				require.NoError(t, cs.Delete(ctx, layers[n].Descriptor.Digest))
			}
			index := cs.Index(manifests...)
			stored := images.Image{Name: "example.com/image:latest", Target: index.Descriptor,
				Labels: map[string]string{crilabels.ImagePlatformLabelKey: test.label}}
			if test.target >= 0 {
				stored.Target = manifests[test.target].Descriptor
			}
			originalTarget := stored.Target
			image, err := ImageForConfig(ctx, client, stored, config.Descriptor.Digest.String())
			require.NoError(t, err)
			assert.Equal(t, manifests[test.want].Descriptor, image.Target())
			assert.Equal(t, test.label, stored.Labels[crilabels.ImagePlatformLabelKey])
			assert.Equal(t, originalTarget, stored.Target)
		})
	}
}
