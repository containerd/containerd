/*
Copyright 2017 The Kubernetes Authors.

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

package server

import (
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	imagestore "github.com/containerd/cri/pkg/store/image"
)

func TestListImages(t *testing.T) {
	c := newTestCRIService()
	imagesInStore := []imagestore.Image{
		{
			ID:          "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID:     "test-chainid-1",
			RepoTags:    []string{"tag-a-1", "tag-b-1"},
			RepoDigests: []string{"digest-a-1", "digest-b-1"},
			Size:        1000,
			ImageSpec: imagespec.Image{
				Config: imagespec.ImageConfig{
					User: "root",
				},
			},
		},
		{
			ID:          "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID:     "test-chainid-2",
			RepoTags:    []string{"tag-a-2", "tag-b-2"},
			RepoDigests: []string{"digest-a-2", "digest-b-2"},
			Size:        2000,
			ImageSpec: imagespec.Image{
				Config: imagespec.ImageConfig{
					User: "1234:1234",
				},
			},
		},
		{
			ID:          "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ChainID:     "test-chainid-3",
			RepoTags:    []string{"tag-a-3", "tag-b-3"},
			RepoDigests: []string{"digest-a-3", "digest-b-3"},
			Size:        3000,
			ImageSpec: imagespec.Image{
				Config: imagespec.ImageConfig{
					User: "nobody",
				},
			},
		},
	}
	expect := []*runtime.Image{
		{
			Id:          "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"tag-a-1", "tag-b-1"},
			RepoDigests: []string{"digest-a-1", "digest-b-1"},
			Size_:       uint64(1000),
			Username:    "root",
		},
		{
			Id:          "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"tag-a-2", "tag-b-2"},
			RepoDigests: []string{"digest-a-2", "digest-b-2"},
			Size_:       uint64(2000),
			Uid:         &runtime.Int64Value{Value: 1234},
		},
		{
			Id:          "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"tag-a-3", "tag-b-3"},
			RepoDigests: []string{"digest-a-3", "digest-b-3"},
			Size_:       uint64(3000),
			Username:    "nobody",
		},
	}

	for _, i := range imagesInStore {
		c.imageStore.Add(i)
	}

	resp, err := c.ListImages(context.Background(), &runtime.ListImagesRequest{})
	assert.NoError(t, err)
	require.NotNil(t, resp)
	images := resp.GetImages()
	assert.Len(t, images, len(expect))
	for _, i := range expect {
		assert.Contains(t, images, i)
	}
}
