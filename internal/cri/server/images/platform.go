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
	"github.com/containerd/platforms"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

// unionPlatforms returns a platforms.MatchComparer that matches any
// platform matched by any of comparers, and, once two platforms have each
// been matched by some comparer - not necessarily the same one, since
// comparers are checked in order and a match is remembered as soon as it
// is found - prefers whichever has more OSFeatures. See the (unexported)
// anyPlatformComparer this mirrors in
// github.com/containerd/platforms/compare.go.
//
// Unlike platforms.Any, which only accepts raw platform specs (and so
// always builds its per-spec matchers the same way), unionPlatforms
// combines full, independently constructed MatchComparers. This is used
// to combine platforms.Default() with a platforms.Only of every
// configured runtime_platforms.<handler>.platform (see ImagePlatform)
// without losing whichever sub-platform compatibility (e.g. amd64 also
// matching 386, see platforms.Only) each individually provides.
func unionPlatforms(comparers ...platforms.MatchComparer) platforms.MatchComparer {
	return unionMatchComparer{comparers: comparers}
}

type unionMatchComparer struct {
	comparers []platforms.MatchComparer
}

func (u unionMatchComparer) Match(p imagespec.Platform) bool {
	for _, c := range u.comparers {
		if c.Match(p) {
			return true
		}
	}
	return false
}

func (u unionMatchComparer) Less(p1, p2 imagespec.Platform) bool {
	var p1m, p2m bool
	for _, c := range u.comparers {
		if !p1m && c.Match(p1) {
			p1m = true
		}
		if !p2m && c.Match(p2) {
			p2m = true
		}
		if p1m && p2m {
			// Both have now been matched by some comparer (not
			// necessarily this one, and not necessarily the same one for
			// each): prefer the one with more OSFeatures, matching
			// platforms.Any's tie-break.
			if len(p1.OSFeatures) != len(p2.OSFeatures) {
				return len(p1.OSFeatures) > len(p2.OSFeatures)
			}
			break
		}
	}

	if !p1m && !p2m && (len(p1.OSFeatures) > 0 || len(p2.OSFeatures) > 0) {
		p1.OSFeatures = nil
		p2.OSFeatures = nil
		return u.Less(p1, p2)
	}

	return p1m && !p2m
}

// imageStorePlatform returns the platforms.MatchComparer used to resolve
// manifests for the CRI image store's in-memory metadata (see
// internal/cri/store/image.Store): platforms.Default(), plus every
// distinct platform configured via runtime_platforms.<handler>.platform.
//
// Without the latter, an image pulled only for a runtime-specific
// platform requiring OSFeatures beyond the host default (e.g. "erofs",
// see the EROFS image layer format specification,
// https://github.com/erofs/erofs-image-spec) would have no manifest
// matching platforms.Default() alone, and so would be unresolvable -
// effectively invisible - through the CRI image store.
func imageStorePlatform(runtimePlatforms map[string]ImagePlatform) platforms.MatchComparer {
	comparers := []platforms.MatchComparer{platforms.Default()}
	seen := map[string]bool{platforms.FormatAll(platforms.DefaultSpec()): true}
	for _, rp := range runtimePlatforms {
		key := platforms.FormatAll(platforms.Normalize(rp.Platform))
		if seen[key] {
			continue
		}
		seen[key] = true
		comparers = append(comparers, platforms.Only(rp.Platform))
	}
	if len(comparers) == 1 {
		return comparers[0]
	}
	return unionPlatforms(comparers...)
}
