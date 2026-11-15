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

import "fmt"

const overlayfsSnapshotter = "overlayfs"

// autoSnapshotterEnabled reports whether CRI selects the snapshotter per
// image: EROFS images go to the erofs snapshotter and all others to the
// default overlayfs snapshotter. It requires overlayfs as the configured
// default and both the erofs snapshotter and differ to be loaded. When it is
// disabled, the returned reason says why.
func autoSnapshotterEnabled(defaultSnapshotter string, erofsSnapshotterLoaded bool, erofsDifferErr error) (bool, string) {
	switch {
	case defaultSnapshotter != overlayfsSnapshotter:
		return false, fmt.Sprintf("default snapshotter is %q, not %q", defaultSnapshotter, overlayfsSnapshotter)
	case !erofsSnapshotterLoaded:
		return false, "erofs snapshotter is not loaded"
	case erofsDifferErr != nil:
		return false, fmt.Sprintf("erofs differ is not loaded: %v", erofsDifferErr)
	}
	return true, ""
}
