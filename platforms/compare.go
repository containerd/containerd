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

type MatchComparer = platforms.MatchComparer

// Only returns a match comparer for a single platform
// using default resolution logic for the platform.
//
// For arm/v8, will also match arm/v7, arm/v6 and arm/v5
// For arm/v7, will also match arm/v6 and arm/v5
// For arm/v6, will also match arm/v5
// For amd64, will also match 386
func Only(platform specs.Platform) MatchComparer {
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
func OnlyStrict(platform specs.Platform) MatchComparer {
	return platforms.OnlyStrict(platform)
}

// Ordered returns a platform MatchComparer which matches any of the platforms
// but orders them in order they are provided.
func Ordered(ps ...specs.Platform) MatchComparer {
	return platforms.Ordered(ps...)
}

// Any returns a platform MatchComparer which matches any of the platforms
// with no preference for ordering.
func Any(ps ...specs.Platform) MatchComparer {
	return platforms.Any(ps...)
}

// All is a platform MatchComparer which matches all platforms
// with preference for ordering.
var All = platforms.All
