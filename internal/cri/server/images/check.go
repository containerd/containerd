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
	"sync"

	"github.com/containerd/containerd/v2/core/images"
	ctrdlabels "github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
)

// Gets all the platform image labels set for an image.
func getPlatformsFromImageLabel(imgLabels map[string]string) []string {
	var platformsForImage []string
	for key, value := range imgLabels {
		if strings.HasPrefix(key, ctrdlabels.PlatformLabelPrefix) {
			platformsForImage = append(platformsForImage, value)
		}
	}
	return platformsForImage
}

// Get list of snapshotters that can be used with platform. By default the list
// always includes the default snapshotter.
func (c *CRIImageService) getSupportedSnapshotsForPlatform(platform string) []string {
	// Always have the default snapshotter for unpacking
	supportedSnapshotters := []string{c.config.Snapshotter}
	for _, imagePlatform := range c.runtimePlatforms {
		if platforms.Format(imagePlatform.Platform) == platform {
			supportedSnapshotters = append(supportedSnapshotters, imagePlatform.Snapshotter)
		}
	}
	return supportedSnapshotters
}

// LoadImages checks all existing images to ensure they are ready to
// be used for CRI. It may try to recover images which are not ready
// but will only log errors, not return any.
func (c *CRIImageService) CheckImages(ctx context.Context) error {
	// TODO: Move way from `client.ListImages` to directly using image store
	cImages, err := c.client.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("unable to list images: %w", err)
	}

	var wg sync.WaitGroup
	for _, i := range cImages {
		wg.Add(1)
		i := i
		// Get list of platform labels this image
		imagePlatforms := getPlatformsFromImageLabel(i.Labels())
		go func(imagePlatforms []string) {
			defer wg.Done()
			for _, imgPlatform := range imagePlatforms {
				// unpackedForSnapshotter gives list of snapshotters that
				// this image was successfully unpacked for.
				var unpackedForSnapshotter []string

				// Support all snapshotters that can be used with this platform
				snapshotters := c.getSupportedSnapshotsForPlatform(imgPlatform)
				for _, snapshotter := range snapshotters {
					ok, _, _, _, err := images.Check(ctx, i.ContentStore(), i.Target(), platforms.Only(platforms.MustParse(imgPlatform)))
					if err != nil {
						log.G(ctx).WithError(err).Errorf("Failed to check image content readiness for %q", i.Name())
						return
					}
					if !ok {
						log.G(ctx).Warnf("The image content readiness for %q is not ok", i.Name())
						// content for this platform matcher was not found. Therefore, remove the image platform
						// label and continue unpacking later images
						break
					}
					// Checking existence of top-level snapshot for each image being recovered.
					// TODO: This logic should be done elsewhere and owned by the image service
					unpacked, err := i.IsUnpacked(ctx, snapshotter)
					if err != nil {
						log.G(ctx).WithError(err).Warnf("Failed to check whether image is unpacked for image %s", i.Name())
						return
					}
					if !unpacked {
						log.G(ctx).Warnf("The image %s is not unpacked.", i.Name())
						// continue and try to unpack with other snapshotters
						continue
					}
					// This image was successfully unpacked for a snapshotter.
					unpackedForSnapshotter = append(unpackedForSnapshotter, snapshotter)
					// Update CRI's image cache
					if err := c.UpdateImage(ctx, i.Name()); err != nil {
						log.G(ctx).WithError(err).Warnf("Failed to update reference for image %q", i.Name())
						return
					}
					log.G(ctx).Debugf("Loaded image %q", i.Name())
				}

				// if this image was not unpacked with any snapshotter, attempt to remove the current imgPlatform label
				if len(unpackedForSnapshotter) == 0 {
					log.G(ctx).Debugf("Failed to unpack image %v for platform label %v. Removing label", i.Name(), imgPlatform)
					ctrdImg, err := c.images.Get(ctx, i.Name())
					if err != nil {
						continue
					}
					_, err = c.deletePlatformLabelAndUpdateImage(ctx, ctrdImg, imgPlatform)
					if err != nil {
						log.G(ctx).Warnf("Failed to remove label %v for image %v", imgPlatform, i.Name())
					}
				}
			}
		}(imagePlatforms)
	}
	wg.Wait()
	return nil
}
