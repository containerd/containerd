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

	"github.com/containerd/errdefs"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ListImages lists existing images.
func (c *GRPCCRIImageService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (*runtime.ListImagesResponse, error) {
	images, err := c.listImages(r.GetFilter())
	if err != nil {
		return nil, err
	}
	return &runtime.ListImagesResponse{Images: images}, nil
}

func (c *CRIImageService) listImages(filter *runtime.ImageFilter) ([]*runtime.Image, error) {
	spec := filter.GetImage()
	var handler, id string
	if spec != nil {
		h, err := c.resolveRuntimeHandler(spec.GetRuntimeHandler())
		if err != nil {
			return nil, err
		}
		handler = h.Name
		if spec.GetImage() != "" {
			image, err := c.LocalResolve(spec.GetImage(), spec.GetRuntimeHandler())
			if err != nil {
				if errdefs.IsNotFound(err) {
					return nil, nil
				}
				return nil, err
			}
			id = image.ID
		}
	}
	seen := make(map[[2]string]bool)
	var result []*runtime.Image
	for _, image := range c.imageStore.List() {
		if image.RuntimeHandler == "" {
			image.RuntimeHandler = c.defaultRuntimeName
		}
		key := [2]string{image.ID, image.RuntimeHandler}
		if seen[key] || (spec != nil && image.RuntimeHandler != handler) || (id != "" && image.ID != id) {
			continue
		}
		if image.RuntimeHandler == c.defaultRuntimeName {
			merged, err := c.GetImage(image.ID, "")
			if err != nil {
				if errdefs.IsNotFound(err) {
					continue
				}
				return nil, err
			}
			image = merged
		}
		seen[key] = true
		result = append(result, c.toCRIImage(image))
	}
	return result, nil
}
