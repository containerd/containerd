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
	"context"
	"fmt"
	"slices"

	"github.com/containerd/platforms"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"

	containerd "github.com/containerd/containerd/v2/client"
	transferimage "github.com/containerd/containerd/v2/core/transfer/image"
)

const (
	// ErofsSnapshotter is the snapshotter used for EROFS images when the
	// snapshotter is selected per image.
	ErofsSnapshotter = "erofs"
	// erofsOSFeature marks a platform whose image has EROFS layers.
	erofsOSFeature = "erofs"
)

// erofsPlatform returns p with the EROFS OS feature added.
func erofsPlatform(p imagespec.Platform) imagespec.Platform {
	p.OSFeatures = append(slices.Clone(p.OSFeatures), erofsOSFeature)
	slices.Sort(p.OSFeatures)
	return p
}

// autoSnapshotterMatcher prefers the EROFS variant of p and falls back to p.
func autoSnapshotterMatcher(p imagespec.Platform) platforms.MatchComparer {
	return platforms.Ordered(erofsPlatform(p), p)
}

// autoSnapshotterStoreOpts returns the transfer store options for pulling an
// image whose snapshotter is selected by the image: the EROFS variant is
// preferred and unpacked into the erofs snapshotter, and any other image is
// unpacked into defaultSnapshotter.
//
// The transfer service matches the default unpack configuration to a plain
// platform only, so the plain configuration is listed first and EROFS images
// fall through to the erofs one.
func autoSnapshotterStoreOpts(p imagespec.Platform, defaultSnapshotter string) []transferimage.StoreOpt {
	ep := erofsPlatform(p)
	return []transferimage.StoreOpt{
		transferimage.WithPlatforms(ep, p),
		// Fetch and unpack only the preferred variant of an index.
		transferimage.WithManifestLimit(1),
		transferimage.WithUnpack(p, defaultSnapshotter),
		transferimage.WithUnpack(ep, ErofsSnapshotter),
	}
}

// unpackedSnapshotter returns the snapshotter an image pulled in auto mode
// was unpacked into. EROFS images are expected in the erofs snapshotter and
// all others in the configured default.
func (c *CRIImageService) unpackedSnapshotter(ctx context.Context, image containerd.Image) (string, error) {
	want := c.config.Snapshotter
	if isErofsImage(ctx, image) {
		want = ErofsSnapshotter
	}
	unpacked, err := image.IsUnpacked(ctx, want)
	if err != nil {
		return "", fmt.Errorf("failed to check whether image %q is unpacked in %q: %w", image.Name(), want, err)
	}
	if !unpacked {
		return "", fmt.Errorf("image %q was not unpacked into snapshotter %q", image.Name(), want)
	}
	return want, nil
}

// isErofsImage reports whether the image config's platform has the EROFS OS
// feature, which is what the unpacker matches on.
func isErofsImage(ctx context.Context, image containerd.Image) bool {
	spec, err := image.Spec(ctx)
	if err != nil {
		return false
	}
	return slices.Contains(spec.OSFeatures, erofsOSFeature)
}
