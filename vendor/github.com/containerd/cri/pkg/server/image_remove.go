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
	"github.com/containerd/containerd/images"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
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

	// Exclude outdated image tag.
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
			// We can only get image id by reading Config from content.
			// If the config is missing, we will fail to get image id,
			// So we won't be able to remove the image forever,
			// and the cri plugin always reports the image is ok.
			// But we also don't check it by manifest,
			// It's possible that two manifest digests have the same image ID in theory.
			// In theory it's possible that an image is compressed with different algorithms,
			// then they'll have the same uncompressed id - image id,
			// but different ids generated from compressed contents - manifest digest.
			// So we decide to leave it.
			// After all, the user can override the repoTag by pulling image again.
			logrus.WithError(err).Errorf("Can't remove image,failed to get config for Image tag %q,id %q", tag, image.ID)
			image.RepoTags = append(image.RepoTags[:i], image.RepoTags[i+1:]...)
			continue
		}
		cID := desc.Digest.String()
		if cID != image.ID {
			logrus.Debugf("Image tag %q for %q is outdated, it's currently used by %q", tag, image.ID, cID)
			image.RepoTags = append(image.RepoTags[:i], image.RepoTags[i+1:]...)
			continue
		}
	}

	// Include all image references, including RepoTag, RepoDigest and id.
	for _, ref := range append(image.RepoTags, image.RepoDigests...) {
		err = c.client.ImageService().Delete(ctx, ref)
		if err == nil || errdefs.IsNotFound(err) {
			continue
		}
		return nil, fmt.Errorf("failed to delete image reference %q for image %q: %v", ref, image.ID, err)
	}
	// Delete image id synchronously to trigger garbage collection.
	err = c.client.ImageService().Delete(ctx, image.ID, images.SynchronousDelete())
	if err != nil && !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to delete image id %q: %v", image.ID, err)
	}
	c.imageStore.Delete(image.ID)
	return &runtime.RemoveImageResponse{}, nil
}
