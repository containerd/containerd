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

package util

import (
	"fmt"

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"

	containerd "github.com/containerd/containerd/v2/client"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
)

// ImageForSnapshotter returns the image to unpack into snapshotter.
//
// An image whose snapshotter was selected at pull time is bound to the
// platform in its CRI platform label when snapshotter is the selected one, so
// that it resolves to the manifest that was unpacked, such as the EROFS
// variant of an index. Only that manifest was fetched, so an image whose
// selected platform has OS features cannot be used with another snapshotter.
// All other images are returned unchanged.
func ImageForSnapshotter(client *containerd.Client, image containerd.Image, snapshotter string) (containerd.Image, error) {
	labels := image.Labels()
	selected := labels[crilabels.ImageSnapshotterLabelKey]
	value := labels[crilabels.ImagePlatformLabelKey]
	if selected == "" || value == "" {
		return image, nil
	}
	p, err := platforms.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("invalid platform label %q on image %q: %w", value, image.Name(), err)
	}
	if selected != snapshotter {
		if len(p.OSFeatures) > 0 {
			return nil, fmt.Errorf("image %q was pulled as %q for snapshotter %q and cannot be used with snapshotter %q, remove the image and pull it again with this runtime handler: %w",
				image.Name(), value, selected, snapshotter, errdefs.ErrFailedPrecondition)
		}
		return image, nil
	}
	return containerd.NewImageWithPlatform(client, image.Metadata(), platforms.Only(p)), nil
}
