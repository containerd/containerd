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

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/metadata/boltutil"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/errdefs"
	bolt "go.etcd.io/bbolt"

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

	handler := imageSpec.GetRuntimeHandler()
	if handler == "" {
		handler = c.defaultRuntimeName
	}
	if err := c.imageMutationLocker.Lock(ctx, handler); err != nil {
		return err
	}
	defer c.imageMutationLocker.Unlock(handler)
	image, err := c.LocalResolve(imageSpec.GetImage(), imageSpec.GetRuntimeHandler())
	if err != nil {
		if errdefs.IsNotFound(err) {
			span.AddEvent(err.Error())
			// return empty without error when image not found.
			return nil
		}
		return fmt.Errorf("can not resolve %q locally: %w", imageSpec.GetImage(), err)
	}
	span.SetAttributes(tracing.Attribute("image.id", image.ID))
	references := c.imageStore.References(image)
	removed := false
	// Remove all image references.
	for _, img := range references {
		ref := img.Name
		err = c.metadata.Update(func(tx *bolt.Tx) error {
			txctx := boltutil.WithTransaction(ctx, tx)
			current, err := c.images.Get(txctx, ref)
			if err != nil {
				return err
			}
			if current.Target.Digest != img.Target.Digest || current.Target.MediaType != img.Target.MediaType ||
				current.Labels[crilabels.ImagePlatformLabelKey] != img.Labels[crilabels.ImagePlatformLabelKey] {
				return errdefs.ErrNotFound
			}
			return c.images.Delete(txctx, ref, images.DeleteTarget(&img.Target))
		})
		if err == nil {
			removed = true
			// Metadata suppresses events inside a supplied transaction.
			if publisher := c.metadata.Publisher(ctx); publisher != nil {
				if err := publisher.Publish(ctx, "/images/delete", &eventstypes.ImageDelete{Name: ref}); err != nil {
					return err
				}
			}
		}
		if err == nil || errdefs.IsNotFound(err) {
			// Update image store to reflect the newest state in containerd.
			if err := c.imageStore.Update(ctx, ref); err != nil && !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to update image reference %q for %q: %w", ref, image.ID, err)
			}
			continue
		}
		return fmt.Errorf("failed to delete image reference %q for %q: %w", ref, image.ID, err)
	}
	if removed && c.gc != nil {
		_, err = c.gc(ctx)
		return err
	}
	return nil
}
