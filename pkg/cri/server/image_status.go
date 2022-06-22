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
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ImageStatus returns the status of the image, returns nil if the image isn't present.
// TODO(random-liu): We should change CRI to distinguish image id and image spec. (See
// kubernetes/kubernetes#46255)
func (c *criService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (*runtime.ImageStatusResponse, error) {
	containerdImage, err := c.localResolve(ctx, r.GetImage().GetImage())
	if err != nil {
		if errdefs.IsNotFound(err) {
			// return empty without error when image not found.
			return &runtime.ImageStatusResponse{}, nil
		}
		return nil, fmt.Errorf("can not resolve %q locally: %w", r.GetImage().GetImage(), err)
	}
	// TODO(random-liu): [P0] Make sure corresponding snapshot exists. What if snapshot
	// doesn't exist?

	imageSpec, err := getImageSpec(ctx, containerdImage)
	if err != nil {
		return nil, err
	}

	imageID, err := getImageID(containerdImage)
	if err != nil {
		return nil, err
	}

	references, err := c.findReferences(ctx, imageID)
	if err != nil {
		return nil, err
	}

	runtimeImage, err := toCRIImage(containerdImage, references)
	if err != nil {
		return nil, err
	}

	info, err := c.toCRIImageInfo(ctx, containerdImage.Metadata(), imageSpec, r.GetVerbose())
	if err != nil {
		return nil, fmt.Errorf("failed to generate image info: %w", err)
	}

	return &runtime.ImageStatusResponse{
		Image: runtimeImage,
		Info:  info,
	}, nil
}

// toCRIImage converts internal image object to CRI runtime.Image.
func toCRIImage(image containerd.Image, references []string) (*runtime.Image, error) {
	imageID, err := getImageID(image)
	if err != nil {
		return nil, err
	}

	var (
		labels    = image.Labels()
		labelSize = labels[imageLabelSize]
		labelUser = labels[imageLabelUser]
	)

	size, err := strconv.ParseUint(labelSize, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image size from str %q: %w", labelSize, err)
	}

	repoTags, repoDigests := parseImageReferences(references)
	runtimeImage := &runtime.Image{
		Id:          imageID,
		RepoTags:    repoTags,
		RepoDigests: repoDigests,
		Size_:       size,
	}

	uid, username := getUserFromImage(labelUser)
	if uid != nil {
		runtimeImage.Uid = &runtime.Int64Value{Value: *uid}
	}
	runtimeImage.Username = username

	return runtimeImage, nil
}

// TODO (mikebrow): discuss moving this struct and / or constants for info map for some or all of these fields to CRI
type verboseImageInfo struct {
	ChainID   string          `json:"chainID"`
	ImageSpec imagespec.Image `json:"imageSpec"`
}

// toCRIImageInfo converts internal image object information to CRI image status response info map.
func (c *criService) toCRIImageInfo(ctx context.Context, image images.Image, imageSpec imagespec.Image, verbose bool) (map[string]string, error) {
	if !verbose {
		return nil, nil
	}

	imi := &verboseImageInfo{
		ChainID:   image.Labels[imageLabelChainID],
		ImageSpec: imageSpec,
	}

	info := make(map[string]string)
	m, err := json.Marshal(imi)
	if err == nil {
		info["info"] = string(m)
	} else {
		log.G(ctx).WithError(err).Errorf("failed to marshal info %v", imi)
		info["info"] = err.Error()
	}

	return info, nil
}
