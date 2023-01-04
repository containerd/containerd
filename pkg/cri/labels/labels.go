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

package labels

const (
	// criContainerdPrefix is common prefix for cri-containerd
	criContainerdPrefix = "io.cri-containerd"
	// ImageLabelKey is the label key indicating the image is managed by cri plugin.
	ImageLabelKey = criContainerdPrefix + ".image"
	// ImageLabelValue is the label value indicating the image is managed by cri plugin.
	ImageLabelValue = "managed"
	// PinnedImageLabelKey is the label value indicating the image is pinned.
	PinnedImageLabelKey = criContainerdPrefix + ".pinned"
	// PinnedImageLabelValue is the label value indicating the image is pinned.
	PinnedImageLabelValue = "pinned"
)
