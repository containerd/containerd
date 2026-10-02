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
	"github.com/containerd/platforms"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

// NodePlatform returns the platform of the node, and is what an unset platform
// means everywhere in the CRI plugin.
func NodePlatform() imagespec.Platform {
	return platforms.DefaultSpec()
}

// PlatformKey returns a stable string identifying a platform, used to key
// images by the platform they were pulled for. An unset platform is the
// platform of the node.
//
// The OS version and the OS features are deliberately left out. On Windows they
// distinguish images that run on the same platform, and choosing between those
// is the job of a platform matcher, not of the key.
func PlatformKey(platform imagespec.Platform) string {
	p := platforms.Normalize(platformOrNode(platform))
	p.OSVersion = ""
	p.OSFeatures = nil
	return platforms.FormatAll(p)
}

// IsNodePlatform reports whether the given platform is the platform of the
// node. An unset platform means the platform of the node.
//
// The platform is compared to the platform of the node rather than run through
// its matcher, because the matcher of the node also accepts platforms the node
// can merely execute: on linux/amd64 it accepts linux/386, and on linux/arm64
// it accepts linux/arm/v7. A runtime_platforms entry naming one of those is
// asking for that platform to be selected, not for the platform of the node.
func IsNodePlatform(platform imagespec.Platform) bool {
	return PlatformKey(platform) == PlatformKey(NodePlatform())
}

// PlatformMatcher returns the matcher to use for an image on the given
// platform.
//
// The platform of the node gets the matcher of the node rather than an exact
// matcher for it. The two are not interchangeable: on Windows the matcher of
// the node selects an image out of an index by OS version, and on darwin it
// also accepts Linux binaries, neither of which platforms.Only does.
func PlatformMatcher(platform imagespec.Platform) platforms.MatchComparer {
	if IsNodePlatform(platform) {
		return platforms.Default()
	}
	return platforms.Only(platform)
}

func platformOrNode(platform imagespec.Platform) imagespec.Platform {
	if platform.OS == "" || platform.Architecture == "" {
		return NodePlatform()
	}
	return platform
}
