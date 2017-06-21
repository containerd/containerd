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

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	containerdmetadata "github.com/containerd/containerd/metadata"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

// RemoveImage removes the image.
// TODO(mikebrow): harden api
// TODO(random-liu): Update CRI to pass image reference instead of ImageSpec. (See
// kubernetes/kubernetes#46255)
// TODO(random-liu): We should change CRI to distinguish image id and image spec.
// Remove the whole image no matter the it's image id or reference. This is the
// semantic defined in CRI now.
func (c *criContainerdService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (retRes *runtime.RemoveImageResponse, retErr error) {
	glog.V(2).Infof("RemoveImage %q", r.GetImage().GetImage())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("RemoveImage %q returns successfully", r.GetImage().GetImage())
		}
	}()
	meta, err := c.localResolve(ctx, r.GetImage().GetImage())
	if err != nil {
		return nil, fmt.Errorf("can not resolve %q locally: %v", r.GetImage().GetImage(), err)
	}
	if meta == nil {
		// return empty without error when image not found.
		return &runtime.RemoveImageResponse{}, nil
	}
	// Include all image references, including RepoTag, RepoDigest and id.
	for _, ref := range append(append(meta.RepoTags, meta.RepoDigests...), meta.ID) {
		// TODO(random-liu): Containerd should schedule a garbage collection immediately,
		// and we may want to wait for the garbage collection to be over here.
		err = c.imageStoreService.Delete(ctx, ref)
		if err == nil || containerdmetadata.IsNotFound(err) {
			continue
		}
		return nil, fmt.Errorf("failed to delete image reference %q for image %q: %v", ref, meta.ID, err)
	}
	if err = c.imageMetadataStore.Delete(meta.ID); err != nil {
		if metadata.IsNotExistError(err) {
			return &runtime.RemoveImageResponse{}, nil
		}
		return nil, fmt.Errorf("an error occurred when delete image %q matadata: %v", meta.ID, err)
	}
	return &runtime.RemoveImageResponse{}, nil
}
