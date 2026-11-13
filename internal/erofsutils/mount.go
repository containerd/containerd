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
	"github.com/opencontainers/go-digest"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
)

// CacheBlobPath returns the layer content cache path for a diffID, following the
// <dir>/<algo>/<xx>/<hex>.erofs layout, where <xx> is the first two characters of
// the encoded digest. The extra prefix directory shards blobs so no single
// directory grows unwieldy for a large cache. It is the single source of the
// layout, shared by the cache producer (ctr build-erofs-cache) and the erofs
// snapshotter that reads it, so the two never drift.
func CacheBlobPath(dir string, diffID digest.Digest) string {
	enc := diffID.Encoded()
	return filepath.Join(dir, diffID.Algorithm().String(), enc[:2], enc+".erofs")
}

// IsErofsMediaType returns true if the media type is a mountable EROFS
// filesystem image layer, i.e. containerd's legacy
// images.MediaTypeErofsLayer or the EROFS image layer format
// specification's images.MediaTypeErofs, either optionally suffixed with
// "+zstd". It does not match images.MediaTypeErofsChunkIndex: a standalone
// chunk-index layer is not itself a mountable EROFS filesystem image.
func IsErofsMediaType(mt string) bool {
	base, _, _ := strings.Cut(mt, "+")
	return base == images.MediaTypeErofsLayer || base == images.MediaTypeErofs
}

// SupportGenerateFromTar reports whether the tar-index conversion mode is
// available. The pure-Go implementation is always available, so this always
// returns (true, nil).
func SupportGenerateFromTar() (bool, error) {
	return true, nil
}

// File names, within an EROFS-snapshotter-managed snapshot directory (see
// plugins/snapshots/erofs), that an EROFS-aware differ (see
// plugins/diff/erofs) writes a committed layer's blob to. Exactly one of
// these is written per layer, chosen by the layer's org.erofs.role
// annotation as defined by the EROFS image layer format specification
// (https://github.com/erofs/erofs-image-spec, §2.4); these constants are
// shared between the two packages so they cannot drift apart.
const (
	// LayerBlobName holds a committed overlay-lower (or role-less) EROFS
	// layer blob: a complete, independently mountable EROFS filesystem
	// image.
	LayerBlobName = "layer.erofs"
	// DataBlobName holds a committed overlay-data-role EROFS layer blob:
	// also a complete EROFS filesystem image, but supplied to the overlay
	// mount as a data-only lower rather than a regular lowerdir.
	DataBlobName = "data.erofs"
	// DeviceBlobName holds a committed device-role layer's raw,
	// uninterpreted byte stream, supplied to the consuming layer's mount
	// via the device= mount option rather than mounted on its own.
	DeviceBlobName = "device.blob"
)

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
