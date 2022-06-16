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

package server

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ListImages lists existing images.
// TODO(random-liu): Add image list filters after CRI defines this more clear, and kubelet
// actually needs it.
func (c *criService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (*runtime.ListImagesResponse, error) {
	list, err := c.client.ListImages(ctx, fmt.Sprintf(`labels."%s"=="%s"`, imageLabelKey, imageLabelValue))
	if err != nil {
		return nil, fmt.Errorf("failed to query image store: %w", err)
	}

	type imageInfo struct {
		image      containerd.Image
		references []string
		spec       imagespec.Image
	}

	// Group by image id and gather image references
	groups := make(map[string]*imageInfo)
	for _, image := range list {
		imageID, err := getImageID(image)
		if err != nil {
			return nil, err
		}

		if existing, ok := groups[imageID]; ok {
			existing.references = append(existing.references, image.Name())
		} else {
			spec, err := getImageSpec(ctx, image)
			if err != nil {
				return nil, err
			}

			groups[imageID] = &imageInfo{
				image:      image,
				references: []string{image.Name()},
				spec:       spec,
			}
		}
	}

	var images []*runtime.Image
	for _, info := range groups {
		// TODO(random-liu): [P0] Make sure corresponding snapshot exists. What if snapshot
		// doesn't exist?
		image, err := toCRIImage(info.image, info.references, info.spec)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
	}

	return &runtime.ListImagesResponse{Images: images}, nil
}
