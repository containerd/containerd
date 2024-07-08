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

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (c *CRIImageService) getRuntimePlatform(runtimeHandler string) *specs.Platform {
	if runtimeHandler == "" || c.config.RuntimePlatforms == nil {
		return nil
	}
	if p, ok := c.config.RuntimePlatforms[runtimeHandler]; ok {
		parsed := platforms.MustParse(p.Platform)
		return &parsed
	}
	return nil
}

// LoadImages checks all existing images to ensure they are ready to
// be used for CRI. It may try to recover images which are not ready
// but will only log errors, not return any.
func (c *CRIImageService) CheckImages(ctx context.Context) error {
	imageList, err := c.images.List(ctx)
	if err != nil {
		return fmt.Errorf("unable to list images: %w", err)
	}

	var wg sync.WaitGroup
	for _, i := range imageList {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Check if image name is a tuple of (ref, runtimeHandler). If it is not,
			// use the default runtime handler
			ref, runtimeHandler := GetRuntimeHandlerFromRef(i.Name)
			platform := platforms.DefaultSpec()
			if rhPlatform := c.getRuntimePlatform(runtimeHandler); rhPlatform != nil {
				platform = *rhPlatform
			}

			platformMatcher := platforms.Only(platform)
			ok, _, _, _, err := images.Check(ctx, c.content, i.Target, platformMatcher)
			if err != nil {
				// TODO: Should we delete this image from containerd store?
				log.G(ctx).WithError(err).Errorf("Failed to check image content readiness for %q", ref)
				return
			}
			if !ok {
				// TODO: Should we delete this image from containerd store?
				log.G(ctx).Warnf("The image content readiness for %q is not ok", ref)
				return
			}
			if err := c.UpdateImage(ctx, i.Name); err != nil {
				log.G(ctx).WithError(err).Warnf("Failed to update reference for image %q", ref)
				return
			}
			log.G(ctx).Debugf("Loaded image %q", i.Name)
		}()
	}
	wg.Wait()
	return nil
}
