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

package plugin

import (
	"testing"

	"github.com/containerd/platforms"
)

// The erofs converter stamps os.features=["erofs"] into the image configs it
// produces, and the client-side snapshotter support check matches that
// platform against the platforms the snapshotter plugin advertises
// (platforms.Any over the introspected plugin platforms, see
// Client.GetSnapshotterSupportedPlatforms). The advertised platforms must
// therefore match both plain default-platform images and converter-stamped
// native erofs images.
func TestSnapshotterPlatforms(t *testing.T) {
	matcher := platforms.Any(snapshotterPlatforms()...)

	if p := platforms.DefaultSpec(); !matcher.Match(p) {
		t.Errorf("advertised platforms do not match the default platform %s", platforms.FormatAll(p))
	}

	// The converter stamps the (always Linux) image config platform.
	stamped := platforms.DefaultSpec()
	stamped.OS = "linux"
	stamped.OSFeatures = []string{"erofs"}
	if !matcher.Match(stamped) {
		t.Errorf("advertised platforms do not match the erofs converter's stamped platform %s", platforms.FormatAll(stamped))
	}
}
