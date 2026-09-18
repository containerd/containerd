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
	"testing"

	"github.com/containerd/platforms"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testForeignPlatform = imagespec.Platform{OS: "linux", Architecture: "mips64le"}

// TestPlatformKeyRejectsMerelyRunnable pins that a platform the node can run
// but which is not its own gets a distinct key. The matcher of the node accepts
// those, so a configured linux/386 on linux/amd64 would otherwise be pulled and
// stored as amd64.
func TestPlatformKeyRejectsMerelyRunnable(t *testing.T) {
	amd64 := imagespec.Platform{OS: "linux", Architecture: "amd64"}
	arm64 := imagespec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}

	for _, tt := range []struct {
		desc string
		a, b imagespec.Platform
		same bool
	}{
		{"same platform", amd64, amd64, true},
		{"arm64 variant is normalized away", imagespec.Platform{OS: "linux", Architecture: "arm64"}, arm64, true},
		{"the OS version is not part of the key",
			imagespec.Platform{OS: "windows", Architecture: "amd64"},
			imagespec.Platform{OS: "windows", Architecture: "amd64", OSVersion: "10.0.20348.1"}, true},
		{"386 is runnable on amd64 but is not amd64", imagespec.Platform{OS: "linux", Architecture: "386"}, amd64, false},
		{"arm/v7 is runnable on arm64 but is not arm64", imagespec.Platform{OS: "linux", Architecture: "arm", Variant: "v7"}, arm64, false},
		{"another OS is a different platform", imagespec.Platform{OS: "windows", Architecture: "amd64"}, amd64, false},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			// The matcher accepts the runnable cases, which is why the key
			// cannot be derived from it.
			if !tt.same && tt.a.OS == tt.b.OS {
				assert.True(t, platforms.Only(tt.b).Match(tt.a),
					"fixture should be one the matcher of the node accepts")
			}
			assert.Equal(t, tt.same, PlatformKey(tt.a) == PlatformKey(tt.b))
		})
	}
}

// TestPlatformKeyIsStable pins that an unset platform and the platform of the
// node produce the same key, since an unset platform means the node.
func TestPlatformKeyIsStable(t *testing.T) {
	assert.Equal(t, PlatformKey(NodePlatform()), PlatformKey(imagespec.Platform{}))
	assert.Equal(t, PlatformKey(imagespec.Platform{}), PlatformKey(imagespec.Platform{OS: "linux"}))
	assert.NotEqual(t, PlatformKey(testForeignPlatform), PlatformKey(imagespec.Platform{}))
}

func TestIsNodePlatform(t *testing.T) {
	assert.True(t, IsNodePlatform(imagespec.Platform{}), "an unset platform is the platform of the node")
	assert.True(t, IsNodePlatform(imagespec.Platform{OS: "linux"}))
	assert.True(t, IsNodePlatform(imagespec.Platform{Architecture: "amd64"}))
	assert.True(t, IsNodePlatform(platforms.DefaultSpec()))
	assert.False(t, IsNodePlatform(testForeignPlatform))
}

// TestPlatformMatcherKeepsNodeMatcher pins that the platform of the node gets
// the matcher of the node and not an exact matcher for it. They are not
// interchangeable: on Windows the matcher of the node selects an image out of
// an index by OS version, and on darwin it also accepts Linux binaries.
func TestPlatformMatcherKeepsNodeMatcher(t *testing.T) {
	nodeSpec := platforms.DefaultSpec()

	node := platforms.Default()
	// Probes chosen to separate the matcher of the node from an exact matcher
	// for it. They only diverge on Windows and darwin, where the matcher of
	// the node carries OS version selection and a Linux fallback; on Linux
	// platforms.Default is defined as platforms.Only of the node platform, so
	// there is nothing to tell apart.
	probes := []imagespec.Platform{
		nodeSpec,
		testForeignPlatform,
		{OS: "linux", Architecture: "386"},
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64", Variant: "v8"},
		{OS: "windows", Architecture: "amd64", OSVersion: "10.0.17763.1"},
		{OS: "windows", Architecture: "amd64", OSVersion: "10.0.20348.1"},
	}

	for _, p := range []imagespec.Platform{{}, nodeSpec} {
		matcher := PlatformMatcher(p)
		require.NotNil(t, matcher)
		for _, probe := range probes {
			assert.Equal(t, node.Match(probe), matcher.Match(probe),
				"matcher for %v must behave like the matcher of the node on %v", p, probe)
		}
	}
}

func TestPlatformMatcherPinsForeignPlatform(t *testing.T) {
	require.False(t, platforms.Default().Match(testForeignPlatform), "fixture must not be the platform of the node")

	matcher := PlatformMatcher(testForeignPlatform)
	require.NotNil(t, matcher)
	assert.True(t, matcher.Match(testForeignPlatform))
	assert.False(t, matcher.Match(platforms.DefaultSpec()))
}
