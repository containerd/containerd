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
	"fmt"
	"golang.org/x/net/context"

	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"
)

// ListImages lists existing images.
// TODO(mikebrow): add filters
// TODO(mikebrow): harden api
func (c *criContainerdService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (*runtime.ListImagesResponse, error) {
	imageMetadataA, err := c.imageMetadataStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list image metadata from store %v", err)
	}
	// TODO(mikebrow): Get the id->tags, and id->digest mapping from our checkpoint;
	// Get other information from containerd image/content store

	var images []*runtime.Image
	for _, image := range imageMetadataA { // TODO(mikebrow): write a ImageMetadata to runtime.Image converter
		ri := &runtime.Image{
			Id:          image.ID,
			RepoTags:    image.RepoTags,
			RepoDigests: image.RepoDigests,
			Size_:       image.Size,
			// TODO(mikebrow): Uid and Username?
		}
		images = append(images, ri)
	}

	return &runtime.ListImagesResponse{Images: images}, nil
}
