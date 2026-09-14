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
	"testing"

	"github.com/containerd/platforms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageStorePlatformNoRuntimePlatformsMatchesDefaultOnly(t *testing.T) {
	p := imageStorePlatform(nil)

	require.True(t, p.Match(platforms.DefaultSpec()))

	erofsSpec := platforms.DefaultSpec()
	erofsSpec.OSFeatures = []string{"erofs"}
	assert.False(t, p.Match(erofsSpec), "an OSFeatures-requiring platform should not match without a configured runtime platform for it")
}

func TestImageStorePlatformMatchesConfiguredRuntimePlatform(t *testing.T) {
	erofsSpec := platforms.DefaultSpec()
	erofsSpec.OSFeatures = []string{"erofs"}

	p := imageStorePlatform(map[string]ImagePlatform{
		"erofs-runtime": {Snapshotter: "erofs", Platform: erofsSpec},
	})

	require.True(t, p.Match(platforms.DefaultSpec()), "the plain default platform must remain matched")
	require.True(t, p.Match(erofsSpec), "the configured erofs runtime platform must be matched")

	// A wholly different architecture must still not match.
	other := platforms.DefaultSpec()
	other.Architecture = "not-a-real-arch"
	assert.False(t, p.Match(other))
}

func TestImageStorePlatformPrefersMostOSFeaturesAmongMatches(t *testing.T) {
	erofsSpec := platforms.DefaultSpec()
	erofsSpec.OSFeatures = []string{"erofs"}

	p := imageStorePlatform(map[string]ImagePlatform{
		"erofs-runtime": {Snapshotter: "erofs", Platform: erofsSpec},
	})

	// Both are matched (a plain platform is a subset of any supported
	// OSFeatures); the erofs one, having more OSFeatures, is preferred.
	assert.True(t, p.Less(erofsSpec, platforms.DefaultSpec()))
	assert.False(t, p.Less(platforms.DefaultSpec(), erofsSpec))
}
