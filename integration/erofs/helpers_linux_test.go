//go:build linux

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

// helpers_linux_test.go provides Linux-only test helpers. These depend on
// packages with Linux-only transitive dependencies (erofsutils → dmverity →
// go-dmverity/keyring).
package erofs

import (
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/converter"
	erofsconv "github.com/containerd/containerd/v2/core/images/converter/erofs"
	"github.com/containerd/platforms"
	"github.com/stretchr/testify/require"
)

// isSnapshotterPlatformError returns true when err is the snapshotter platform
// support check failure. This occurs when the snapshotter has not declared
// support for the image's platform (e.g. os.features=["erofs"]). Tests that
// hit this error skip until the snapshotter advertises the full platform set.
func isSnapshotterPlatformError(err error) bool {
	return err != nil && containsFrag(err.Error(), "does not support platform")
}

// isErofsMediaTypePrefix returns true for any media type that starts with
// "application/vnd.erofs". This covers both the canonical types
// (vnd.erofs, vnd.erofs+zstd) and the legacy layer type (vnd.erofs.layer.v1).
func isErofsMediaTypePrefix(mt string) bool {
	const prefix = "application/vnd.erofs"
	return len(mt) >= len(prefix) && mt[:len(prefix)] == prefix
}

// erofsTestImage is the small, publicly available tar image used as the seed
// for local EROFS conversions. Kept small to minimise download time.
const erofsTestImage = "ghcr.io/containerd/alpine:3.14.0"

// localEROFS fetches erofsTestImage and converts it to a per-layer EROFS+zstd
// image using the local converter (no registry push). The converted image is
// stored as dstRef. Both images are cleaned up on test exit.
func localEROFS(t *testing.T, c *containerd.Client, dstRef string) *images.Image {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()

	_, err := c.Fetch(ctx, erofsTestImage,
		containerd.WithPlatform(platforms.DefaultString()))
	if err != nil {
		if isNetworkError(err) {
			t.Skipf("seed image %s not reachable: %v", erofsTestImage, err)
		}
		require.NoError(t, err, "fetch seed image for local EROFS conversion")
	}
	t.Cleanup(func() {
		ctx2, c2 := testContext(t)
		defer c2()
		_ = c.ImageService().Delete(ctx2, erofsTestImage, images.SynchronousDelete())
	})

	opts := []converter.Opt{
		converter.WithLayerConvertFunc(erofsconv.LayerConvertFunc(
			erofsconv.WithBlobCompression("zstd"))),
		converter.WithUpdateManifest(erofsconv.UpdateManifestPlatform),
		converter.WithPlatform(platforms.DefaultStrict()),
	}
	img, err := converter.Convert(ctx, c, dstRef, erofsTestImage, opts...)
	require.NoError(t, err, "convert to per-layer EROFS+zstd")
	t.Cleanup(func() {
		ctx2, c2 := testContext(t)
		defer c2()
		_ = c.ImageService().Delete(ctx2, dstRef, images.SynchronousDelete())
	})
	return img
}
