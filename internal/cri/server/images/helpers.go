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

	"github.com/pkg/errors"

	"github.com/containerd/containerd/v2/core/images"
	ctrdlabels "github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/platforms"
)

func (c *CRIImageService) deletePlatformLabelAndUpdateImage(ctx context.Context, ctrdImg images.Image, platform string) (images.Image, error) {
	if platform == "" {
		// use default platform of host
		platform = platforms.Format(platforms.DefaultSpec())
	}
	platformLabelKey := fmt.Sprintf(ctrdlabels.PlatformLabelFormat, ctrdlabels.PlatformLabelPrefix, platform)

	if _, ok := ctrdImg.Labels[platformLabelKey]; ok {
		delete(ctrdImg.Labels, platformLabelKey)
		updatedImg, err := c.images.Update(ctx, ctrdImg, "labels")
		if err != nil {
			return images.Image{}, errors.Wrapf(err, "failed to update imageRef %v after label delete", ctrdImg.Name)
		}
		return updatedImg, nil
	}
	return images.Image{}, fmt.Errorf("platform label %v does not exist for %v", platform, ctrdImg.Name)
}
