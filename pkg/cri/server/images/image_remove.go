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
	"strings"

	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/images"
	ctrdlabels "github.com/containerd/containerd/v2/labels"
	"github.com/containerd/containerd/v2/tracing"
	"github.com/pkg/errors"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func runtimeHandlerLabelExists(labels map[string]string) bool {
	runtimeHandlerLabelExists := false
	for imageLabelKey, _ := range labels {
		if strings.HasPrefix(imageLabelKey, ctrdlabels.RuntimeHandlerLabelPrefix) {
			runtimeHandlerLabelExists = true
			break
		}
	}
	return runtimeHandlerLabelExists
}

// RemoveImage removes the image.
// TODO(random-liu): Update CRI to pass image reference instead of ImageSpec. (See
// kubernetes/kubernetes#46255)
// TODO(random-liu): We should change CRI to distinguish image id and image spec.
// Remove the whole image no matter the it's image id or reference. This is the
// semantic defined in CRI now.
func (c *CRIImageService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	span := tracing.SpanFromContext(ctx)

	// evaluate runtime handler from request and validate.
	// if no runtime handler is specified, default runtime handler is used.
	runtimeHdlr := r.Image.GetRuntimeHandler()
	if runtimeHdlr == "" {
		runtimeHdlr = c.config.ContainerdConfig.DefaultRuntimeName
	}
	// validate the runtimehandler to use for this image pull
	_, ok := c.config.ContainerdConfig.Runtimes[runtimeHdlr]
	if !ok {
		return nil, fmt.Errorf("no runtime for %q is configured", runtimeHdlr)
	}

	image, err := c.LocalResolve(r.GetImage().GetImage(), runtimeHdlr)
	if err != nil {
		if errdefs.IsNotFound(err) {
			span.AddEvent(err.Error())
			// return empty without error when image not found.
			return &runtime.RemoveImageResponse{}, nil
		}
		return nil, fmt.Errorf("can not resolve %q locally: %w", r.GetImage().GetImage(), err)
	}
	span.SetAttributes(tracing.Attribute("image.id", image.Key.ID))
	// Remove all image references.
	for _, ref := range image.References {
		var opts []images.DeleteOpt

		// Remove the label from containerd DB for this image
		var updatedImg images.Image
		ctrdImg, err := c.client.ImageService().Get(ctx, ref)
		if err == nil {
			// remove the runtimeHandler label from containerd image
			runtimeHandlerKey := fmt.Sprintf(ctrdlabels.RuntimeHandlerLabelFormat, ctrdlabels.RuntimeHandlerLabelPrefix, runtimeHdlr)
			if ctrdImg.Labels[runtimeHandlerKey] != "" {
				delete(ctrdImg.Labels, runtimeHandlerKey)
				updatedImg, err = c.client.ImageService().Update(ctx, ctrdImg, "labels", "newRuntimeHandler."+runtimeHdlr)
				if err != nil {
					return nil, errors.Wrapf(err, "failed to remove imageRef %v", ref)
				}
			}
		}

		// delete ref from CRI image store
		err = c.imageStore.Update(ctx, ref, runtimeHdlr)
		// Update image store to reflect the newest state in containerd.
		if err != nil {
			return nil, fmt.Errorf("failed to update image reference %q for %q: %w", ref, image.Key.ID, err)
		}

		if !runtimeHandlerLabelExists(updatedImg.Labels) {
			// we removed the last runtime handler reference, so completely remove this image from containerd store
			opts = []images.DeleteOpt{images.SynchronousDelete(), images.RuntimeHandler(runtimeHdlr)}

			err = c.client.ImageService().Delete(ctx, ref, opts...)
			if err == nil || errdefs.IsNotFound(err) {
				// Update image store to reflect the newest state in containerd.
				if err := c.imageStore.Update(ctx, ref, runtimeHdlr); err != nil {
					return nil, fmt.Errorf("failed to update image reference %q for %q: %w", ref, image.Key.ID, err)
				}
				continue
			}
			return nil, fmt.Errorf("failed to delete image reference %q for %q: %w", ref, image.Key.ID, err)
		}
	}
	return &runtime.RemoveImageResponse{}, nil
}
