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
	"sync"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/log"
)

// CheckImages checks all existing images to ensure they are ready to
// be used for CRI. It may try to recover images which are not ready
// but will only log errors, not return any.
func (c *CRIImageService) CheckImages(ctx context.Context) error {
	// TODO: Move way from `client.ListImages` to directly using image store
	cImages, err := c.client.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("unable to list images: %w", err)
	}

	// TODO: Support all snapshotter
	snapshotter := c.config.Snapshotter
	var wg sync.WaitGroup
	for _, i := range cImages {
		wg.Go(func() {
			// TODO: Check platform/snapshot combination. Snapshot check should come first
			onNodePlatform, matched, err := c.checkImagePlatforms(ctx, i)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to check image content readiness for %q", i.Name())
				return
			}
			if !matched {
				log.G(ctx).Warnf("The image content readiness for %q is not ok", i.Name())
				return
			}
			if onNodePlatform {
				// Checking existence of top-level snapshot for each image being recovered.
				// TODO: This logic should be done elsewhere and owned by the image service
				unpacked, err := i.IsUnpacked(ctx, snapshotter)
				if err != nil {
					log.G(ctx).WithError(err).Warnf("Failed to check whether image is unpacked for image %s", i.Name())
					return
				}
				if !unpacked {
					log.G(ctx).Warnf("The image %s is not unpacked.", i.Name())
					// TODO(random-liu): Consider whether we should try unpack here.
				}
			} else {
				// The image is only present for a platform configured through
				// runtime_platforms. The containerd client resolves images
				// against the platform of the node, so the top-level snapshot
				// cannot be checked here. The image still has to be recovered,
				// otherwise ImageStatus would not report it and the kubelet
				// would keep pulling it.
				log.G(ctx).Debugf("Skipping unpack check for image %q, which is not present for the platform of the node", i.Name())
			}
			if err := c.UpdateImage(ctx, i.Name()); err != nil {
				log.G(ctx).WithError(err).Warnf("Failed to update reference for image %q", i.Name())
				return
			}
			log.G(ctx).Debugf("Loaded image %q", i.Name())
		})
	}
	wg.Wait()
	return nil
}

// checkImagePlatforms reports whether the content of the image is complete for
// at least one of the platforms the image service resolves images for, and
// whether the platform that matched is the platform of the node.
//
// An image is only present locally for the platforms it was actually pulled
// for, so an image pulled for a platform configured through runtime_platforms
// would not be recovered if only the platform of the node were checked.
func (c *CRIImageService) checkImagePlatforms(ctx context.Context, i containerd.Image) (onNodePlatform, matched bool, err error) {
	for idx, matcher := range c.platformMatchers() {
		ok, _, _, _, err := images.Check(ctx, i.ContentStore(), i.Target(), matcher)
		if err != nil {
			return false, false, err
		}
		if ok {
			// platformMatchers always returns the platform of the node first.
			return idx == 0, true, nil
		}
	}
	return false, false, nil
}
