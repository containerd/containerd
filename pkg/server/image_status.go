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
	"golang.org/x/net/context"

	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"
)

// ImageStatus returns the status of the image, returns nil if the image isn't present.
func (c *criContainerdService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (*runtime.ImageStatusResponse, error) {
	ref := r.GetImage().GetImage()
	// TODO(mikebrow): Get the id->tags, and id->digest mapping from our checkpoint;
	// Get other information from containerd image/content store

	// if the passed ref is a digest.. and is stored the following get should work
	// note: get returns nil with no err
	meta, _ := c.imageMetadataStore.Get(ref)
	if meta != nil {
		return &runtime.ImageStatusResponse{Image: &runtime.Image{ // TODO(mikebrow): write a ImageMetadata to runtim.Image converter
			Id:          meta.ID,
			RepoTags:    meta.RepoTags,
			RepoDigests: meta.RepoDigests,
			Size_:       meta.Size,
			// TODO(mikebrow): Uid and Username?
		}}, nil
	}

	// Search for image by ref in repo tags if found the ID matching ref
	// is our digest.
	imageMetadataA, err := c.imageMetadataStore.List()
	if err == nil {
		for _, meta := range imageMetadataA {
			for _, tag := range meta.RepoTags {
				if ref == tag {
					return &runtime.ImageStatusResponse{Image: &runtime.Image{ // TODO(mikebrow): write a ImageMetadata to runtim.Image converter
						Id:          meta.ID,
						RepoTags:    meta.RepoTags,
						RepoDigests: meta.RepoDigests,
						Size_:       meta.Size,
						// TODO(mikebrow): Uid and Username?
					}}, nil
				}
			}
		}
	}

	return nil, nil
}
