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

	"github.com/containerd/containerd/errdefs"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

// RemoveImage removes the image.
// TODO(random-liu): Update CRI to pass image reference instead of ImageSpec. (See
// kubernetes/kubernetes#46255)
// TODO(random-liu): We should change CRI to distinguish image id and image spec.
// Remove the whole image no matter the it's image id or reference. This is the
// semantic defined in CRI now.
func (c *criContainerdService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	image, err := c.localResolve(ctx, r.GetImage().GetImage())
	if err != nil {
		return nil, fmt.Errorf("can not resolve %q locally: %v", r.GetImage().GetImage(), err)
	}
	if image == nil {
		// return empty without error when image not found.
		return &runtime.RemoveImageResponse{}, nil
	}

	// Exclude out dated image tag.
	for i, tag := range image.RepoTags {
		cImage, err := c.client.GetImage(ctx, tag)
		if err != nil {
			if errdefs.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("failed to get image %q: %v", tag, err)
		}
		desc, err := cImage.Config(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get image %q config descriptor: %v", tag, err)
		}
		cID := desc.Digest.String()
		if cID == image.ID {
			continue
		}
		glog.V(4).Infof("Image tag %q for %q is out dated, it's currently used by %q", tag, image.ID, cID)
		image.RepoTags = append(image.RepoTags[:i], image.RepoTags[i+1:]...)
	}

	// Include all image references, including RepoTag, RepoDigest and id.
	for _, ref := range append(append(image.RepoTags, image.RepoDigests...), image.ID) {
		// TODO(random-liu): Containerd should schedule a garbage collection immediately,
		// and we may want to wait for the garbage collection to be over here.
		err = c.imageStoreService.Delete(ctx, ref)
		if err == nil || errdefs.IsNotFound(err) {
			continue
		}
		return nil, fmt.Errorf("failed to delete image reference %q for image %q: %v", ref, image.ID, err)
	}
	c.imageStore.Delete(image.ID)
	return &runtime.RemoveImageResponse{}, nil
}
