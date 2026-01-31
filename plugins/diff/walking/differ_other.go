//go:build !linux

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

package walking

import (
	"context"
	"errors"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
)

// getOverlayUpperDir returns empty string on non-Linux platforms
// as overlay filesystem is Linux-specific.
func getOverlayUpperDir(mounts []mount.Mount) string {
	return ""
}

// compareOverlay is not supported on non-Linux platforms.
// This function should never be called since getOverlayUpperDir always returns "".
func (s *walkingDiff) compareOverlay(ctx context.Context, lower []mount.Mount, upperDir string, compressionType compression.Compression, config *diff.Config) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, errors.New("overlay diff not supported on this platform")
}
