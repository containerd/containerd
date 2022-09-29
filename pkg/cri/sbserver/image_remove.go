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

package sbserver

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// RemoveImage removes the image.
// TODO(random-liu): Update CRI to pass image reference instead of ImageSpec. (See
// kubernetes/kubernetes#46255)
// TODO(random-liu): We should change CRI to distinguish image id and image spec.
// Remove the whole image no matter the it's image id or reference. This is the
// semantic defined in CRI now.
func (c *criService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	image, err := c.localResolve(r.GetImage().GetImage())
	if err != nil {
		if errdefs.IsNotFound(err) {
			// return empty without error when image not found.
			return &runtime.RemoveImageResponse{}, nil
		}
		return nil, fmt.Errorf("can not resolve %q locally: %w", r.GetImage().GetImage(), err)
	}

	// Remove all image references.
	for i, ref := range image.References {
		var opts []images.DeleteOpt
		if i == len(image.References)-1 {
			// Delete the last image reference synchronously to trigger garbage collection.
			// This is the best effort. It is possible that the image reference is deleted by
			// someone else before this point.
			opts = []images.DeleteOpt{images.SynchronousDelete()}
		}
		err = c.client.ImageService().Delete(ctx, ref, opts...)
		if err == nil || errdefs.IsNotFound(err) {
			// Update image store to reflect the newest state in containerd.
			if err := c.imageStore.Update(ctx, ref); err != nil {
				return nil, fmt.Errorf("failed to update image reference %q for %q: %w", ref, image.ID, err)
			}
			continue
		}
		return nil, fmt.Errorf("failed to delete image reference %q for %q: %w", ref, image.ID, err)
	}
	return &runtime.RemoveImageResponse{}, nil
}
