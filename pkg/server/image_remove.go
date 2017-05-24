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

// RemoveImage removes the image.
// TODO(mikebrow): harden api
func (c *criContainerdService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	// Only remove image from the internal metadata store for now.
	// Note that the image must be digest here in current implementation.
	// TODO(mikebrow): remove the image via containerd
	err := c.imageMetadataStore.Delete(r.GetImage().GetImage())
	return &runtime.RemoveImageResponse{}, err
}
