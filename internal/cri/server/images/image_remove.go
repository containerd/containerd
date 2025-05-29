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

package images

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/errdefs"

	reference "github.com/distribution/reference"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// RemoveImage removes the image.
// TODO(random-liu): Update CRI to pass image reference instead of ImageSpec. (See
// kubernetes/kubernetes#46255)
// TODO(random-liu): We should change CRI to distinguish image id and image spec.
// Remove the whole image no matter the it's image id or reference. This is the
// semantic defined in CRI now.
func (c *GRPCCRIImageService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	err := c.CRIImageService.RemoveImage(ctx, r.GetImage())
	if err != nil && !errdefs.IsNotFound(err) {
		return nil, err
	}
	return &runtime.RemoveImageResponse{}, nil
}

func (c *CRIImageService) RemoveImage(ctx context.Context, imageSpec *runtime.ImageSpec) error {
	span := tracing.SpanFromContext(ctx)

	image, err := c.LocalResolve(imageSpec.GetImage())
	if err != nil {
		if errdefs.IsNotFound(err) {
			span.AddEvent(err.Error())
			// return empty without error when image not found.
			return nil
		}
		return fmt.Errorf("can not resolve %q locally: %w", imageSpec.GetImage(), err)
	}

	span.SetAttributes(tracing.Attribute("image.id", image.ID))

	// Remove all image references.
	for i, ref := range image.References {
		var opts []images.DeleteOpt
		if i == len(image.References)-1 {
			// Delete the last image reference synchronously to trigger garbage collection.
			// This is best effort. It is possible that the image reference is deleted by
			// someone else before this point.
			opts = []images.DeleteOpt{images.SynchronousDelete()}
		}

		// If ref is an image name, it is normalized (includes the registry prefix).
		if err := c.removeImageAndRef(ctx, ref, image.ID, opts); err != nil {
			return err
		}

		name, err := reference.ParseNormalizedNamed(ref)
		if err != nil {
			continue
		}

		familiarName := reference.FamiliarString(name)
		if familiarName != ref {
			// If the image was stored with a familiar name (e.g. "busybox:latest"),
			// remove that too.
			if err := c.removeImageAndRef(ctx, familiarName, image.ID, opts); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *CRIImageService) removeImageAndRef(ctx context.Context, ref, imageID string, opts []images.DeleteOpt) error {
	err := c.images.Delete(ctx, ref, opts...)
	if err == nil || errdefs.IsNotFound(err) {
		// Update image store to reflect the newest state in containerd.
		if err := c.imageStore.Update(ctx, ref); err != nil {
			return fmt.Errorf("failed to update image reference %q for %q: %w", ref, imageID, err)
		}
		return nil
	}
	return fmt.Errorf("failed to delete image reference %q for %q: %w", ref, imageID, err)
}
