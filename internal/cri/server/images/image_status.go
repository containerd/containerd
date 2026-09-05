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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ImageStatus returns the status of the image, returns nil if the image isn't present.
// TODO(random-liu): We should change CRI to distinguish image id and image spec. (See
// kubernetes/kubernetes#46255)
func (c *CRIImageService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (*runtime.ImageStatusResponse, error) {
	runtimeHandler := r.GetImage().GetRuntimeHandler()
	image, err := c.LocalResolve(r.GetImage().GetImage(), c.PlatformForImage(r.GetImage().GetImage(), runtimeHandler))
	if err != nil {
		if errdefs.IsNotFound(err) {
			// return empty without error when image not found.
			return &runtime.ImageStatusResponse{}, nil
		}
		return nil, fmt.Errorf("can not resolve %q locally: %w", r.GetImage().GetImage(), err)
	}

	// Asked about a runtime handler, the question is whether the image is
	// usable by it, which needs it unpacked in the snapshotter of that
	// handler. Reporting it as absent otherwise makes the caller pull it,
	// which unpacks it in the right snapshotter.
	//
	// Without a handler the question is whether the image is known at all,
	// which is what it has always meant. An image that exists but is not
	// unpacked, as one pulled outside CRI, stays visible.
	//
	// A failure to determine that is not a reason to fail the request: a
	// snapshotter that cannot be reached fails loudly on pull and on
	// container creation.
	if runtimeHandler != "" {
		snapshotter := c.SnapshotterForRuntimeHandler(runtimeHandler)
		switch unpacked, err := c.IsImageUnpacked(ctx, image, snapshotter); {
		case err != nil:
			log.G(ctx).WithError(err).Warnf("Failed to check whether image %q is unpacked in snapshotter %q", image.ID, snapshotter)
		case !unpacked:
			log.G(ctx).Debugf("Image %q is not unpacked in snapshotter %q, reporting it as absent", image.ID, snapshotter)
			return &runtime.ImageStatusResponse{}, nil
		}
	}

	runtimeImage := toCRIImage(image, runtimeHandler)
	info, err := c.toCRIImageInfo(ctx, &image, r.GetVerbose())
	if err != nil {
		return nil, fmt.Errorf("failed to generate image info: %w", err)
	}

	return &runtime.ImageStatusResponse{
		Image: runtimeImage,
		Info:  info,
	}, nil
}

// toCRIImage converts internal image object to CRI runtime.Image.
func toCRIImage(image imagestore.Image, runtimeHandler string) *runtime.Image {
	repoTags, repoDigests := util.ParseImageReferences(image.References)
	runtimeImage := &runtime.Image{
		Id:          image.ID,
		RepoTags:    repoTags,
		RepoDigests: repoDigests,
		Size:        uint64(image.Size),
		Pinned:      image.Pinned,
	}
	if runtimeHandler != "" {
		// Answer for the runtime handler that was asked about, so the caller
		// can tell which one this status is for.
		runtimeImage.Spec = &runtime.ImageSpec{
			Image:          image.ID,
			RuntimeHandler: runtimeHandler,
		}
	}
	uid, username := getUserFromImage(image.ImageSpec.Config.User)
	if uid != nil {
		runtimeImage.Uid = &runtime.Int64Value{Value: *uid}
	}
	runtimeImage.Username = username

	return runtimeImage
}

// getUserFromImage gets uid or user name of the image user.
// If user is numeric, it will be treated as uid; or else, it is treated as user name.
func getUserFromImage(user string) (*int64, string) {
	// return both empty if user is not specified in the image.
	if user == "" {
		return nil, ""
	}
	// split instances where the id may contain user:group
	user = strings.Split(user, ":")[0]
	// user could be either uid or user name. Try to interpret as numeric uid.
	uid, err := strconv.ParseInt(user, 10, 64)
	if err != nil {
		// If user is non numeric, assume it's user name.
		return nil, user
	}
	// If user is a numeric uid.
	return &uid, ""
}

// TODO (mikebrow): discuss moving this struct and / or constants for info map for some or all of these fields to CRI
type verboseImageInfo struct {
	ChainID   string          `json:"chainID"`
	ImageSpec imagespec.Image `json:"imageSpec"`
}

// toCRIImageInfo converts internal image object information to CRI image status response info map.
func (c *CRIImageService) toCRIImageInfo(ctx context.Context, image *imagestore.Image, verbose bool) (map[string]string, error) {
	if !verbose {
		return nil, nil
	}

	info := make(map[string]string)

	imi := &verboseImageInfo{
		ChainID:   image.ChainID,
		ImageSpec: image.ImageSpec,
	}

	m, err := json.Marshal(imi)
	if err == nil {
		info["info"] = string(m)
	} else {
		log.G(ctx).WithError(err).Errorf("failed to marshal info %v", imi)
		info["info"] = err.Error()
	}

	return info, nil
}
