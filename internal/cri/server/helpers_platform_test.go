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

package server

import (
	"testing"

	"github.com/containerd/platforms"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContainerImagePlatform pins that only an image resolved for a platform
// other than the platform of the node replaces the matcher of the client.
// Replacing it for the platform of the node would drop the matching the client
// does, including the OS version handling Windows needs to pick an image out of
// an index.
func TestContainerImagePlatform(t *testing.T) {
	t.Run("unset platform keeps the client matcher", func(t *testing.T) {
		assert.Nil(t, containerImagePlatform(imagespec.Platform{}))
		assert.Nil(t, containerImagePlatform(imagespec.Platform{OS: "linux"}))
		assert.Nil(t, containerImagePlatform(imagespec.Platform{Architecture: "amd64"}))
	})

	t.Run("platform of the node keeps the client matcher", func(t *testing.T) {
		assert.Nil(t, containerImagePlatform(platforms.DefaultSpec()))
	})

	t.Run("foreign platform is pinned", func(t *testing.T) {
		foreign := imagespec.Platform{OS: "linux", Architecture: "mips64le"}
		require.False(t, platforms.Default().Match(foreign), "fixture must not be the platform of the node")

		matcher := containerImagePlatform(foreign)
		require.NotNil(t, matcher)
		assert.True(t, matcher.Match(foreign))
		assert.False(t, matcher.Match(platforms.DefaultSpec()))
	})
}
