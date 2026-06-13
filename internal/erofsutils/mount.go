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

// This file contains the public erofsutils API. All conversion paths now use
// pure-Go implementations via go-erofs + continuity/tarconv; no external
// process is spawned.
package erofsutils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/mount"
)

// IsErofsMediaType returns true if the media type is an EROFS layer type.
// Recognises both the canonical application/vnd.erofs[+zstd] types and the
// legacy application/vnd.erofs.layer.v1[+zstd] aliases.
func IsErofsMediaType(mt string) bool {
	return strings.HasPrefix(mt, "application/vnd.erofs")
}

// SupportGenerateFromTar reports whether the tar-index conversion mode is
// available. The pure-Go implementation is always available, so this always
// returns (true, nil).
func SupportGenerateFromTar() (bool, error) {
	return true, nil
}

// MountsToLayer returns the snapshot layer directory in order to generate
// EROFS-formatted blobs.
//
// The candidate directory is checked for ".erofslayer" to confirm that this
// active snapshot was created by the EROFS snapshotter rather than another
// snapshotter.
func MountsToLayer(mounts []mount.Mount) (string, error) {
	var layer string

	// If mount[0].Type is prefixed with "mkfs/", it is always the snapshot layer.
	if strings.HasPrefix(mounts[0].Type, "mkfs/") {
		layer = filepath.Dir(mounts[0].Source)
	} else {
		// Otherwise check the last mount entry.
		mnt := mounts[len(mounts)-1]
		mt := strings.Split(mnt.Type, "/")

		switch mt[len(mt)-1] {
		case "bind", "erofs":
			layer = filepath.Dir(mnt.Source)
		case "overlay":
			var topLower string
			for _, o := range mnt.Options {
				if k, v, ok := strings.Cut(o, "="); ok {
					switch k {
					case "upperdir":
						layer = filepath.Dir(v)
					case "lowerdir":
						// Use the first mount source for the top lower layer.
						topLower = filepath.Dir(mounts[0].Source)
					}
				}
			}
			if layer == "" {
				if topLower == "" {
					return "", fmt.Errorf("unsupported overlay layer for erofs differ: %w", errdefs.ErrNotImplemented)
				}
				layer = topLower
			}
		default:
			return "", fmt.Errorf("invalid filesystem type %q for erofs differ: %w", mnt.Type, errdefs.ErrNotImplemented)
		}
	}
	// If the layer is not prepared by the EROFS snapshotter, fall back to the next differ.
	if _, err := os.Stat(filepath.Join(layer, ".erofslayer")); err != nil {
		return "", fmt.Errorf("mount layer type must be erofs-layer: %w", errdefs.ErrNotImplemented)
	}
	return layer, nil
}
