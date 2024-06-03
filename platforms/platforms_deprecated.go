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

package platforms

import (
	"github.com/containerd/platforms"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Platform is a type alias for convenience, so there is no need to import image-spec package everywhere.
type Platform = specs.Platform

// DefaultSpec returns the current platform's default platform specification.
func DefaultSpec() specs.Platform {
	return platforms.DefaultSpec()
}

// Default returns the default matcher for the platform.
func Default() platforms.MatchComparer {
	return platforms.Default()
}

// DefaultString returns the default string specifier for the platform.
func DefaultString() string {
	return platforms.DefaultString()
}

// DefaultStrict returns strict form of Default.
func DefaultStrict() MatchComparer {
	return platforms.DefaultStrict()
}

// MatchComparer is able to match and compare platforms to
// filter and sort platforms.
type MatchComparer = platforms.MatchComparer

// Matcher matches platforms specifications, provided by an image or runtime.
type Matcher = platforms.Matcher

// NewMatcher returns a simple matcher based on the provided platform
// specification. The returned matcher only looks for equality based on os,
// architecture and variant.
//
// One may implement their own matcher if this doesn't provide the required
// functionality.
//
// Applications should opt to use `Match` over directly parsing specifiers.
func NewMatcher(platform specs.Platform) platforms.Matcher {
	return platforms.NewMatcher(platform)
}

// Parse parses the platform specifier syntax into a platform declaration.
//
// Platform specifiers are in the format `<os>|<arch>|<os>/<arch>[/<variant>]`.
// The minimum required information for a platform specifier is the operating
// system or architecture. If there is only a single string (no slashes), the
// value will be matched against the known set of operating systems, then fall
// back to the known set of architectures. The missing component will be
// inferred based on the local environment.
func Parse(specifier string) (specs.Platform, error) {
	return platforms.Parse(specifier)
}

// MustParse is like Parses but panics if the specifier cannot be parsed.
// Simplifies initialization of global variables.
func MustParse(specifier string) specs.Platform {
	return platforms.MustParse(specifier)
}

// Format returns a string specifier from the provided platform specification.
func Format(platform specs.Platform) string {
	return platforms.Format(platform)
}

// Normalize validates and translate the platform to the canonical value.
//
// For example, if "Aarch64" is encountered, we change it to "arm64" or if
// "x86_64" is encountered, it becomes "amd64".
func Normalize(platform specs.Platform) specs.Platform {
	return platforms.Normalize(platform)
}

// Only returns a match comparer for a single platform
// using default resolution logic for the platform.
//
// For arm/v8, will also match arm/v7, arm/v6 and arm/v5
// For arm/v7, will also match arm/v6 and arm/v5
// For arm/v6, will also match arm/v5
// For amd64, will also match 386
func Only(platform specs.Platform) platforms.MatchComparer {
	return platforms.Only(platform)
}

// OnlyStrict returns a match comparer for a single platform.
//
// Unlike Only, OnlyStrict does not match sub platforms.
// So, "arm/vN" will not match "arm/vM" where M < N,
// and "amd64" will not also match "386".
//
// OnlyStrict matches non-canonical forms.
// So, "arm64" matches "arm/64/v8".
func OnlyStrict(platform specs.Platform) platforms.MatchComparer {
	return platforms.OnlyStrict(platform)
}

// Ordered returns a platform MatchComparer which matches any of the platforms
// but orders them in order they are provided.
func Ordered(platform ...specs.Platform) platforms.MatchComparer {
	return platforms.Ordered(platform...)
}

// Any returns a platform MatchComparer which matches any of the platforms
// with no preference for ordering.
func Any(platform ...specs.Platform) platforms.MatchComparer {
	return platforms.Any(platform...)
}

// All is a platform MatchComparer which matches all platforms
// with preference for ordering.
var All = platforms.All

func GetWindowsOsVersion() string {
	return getWindowsOsVersion()
}
