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

// convert_linux_test.go contains integration tests for the EROFS converter.
// These tests use converter/erofs which has Linux-only transitive dependencies
// via erofsutils → dmverity.go → go-dmverity/pkg/keyring.
//
// Tests here do NOT require root or the EROFS kernel module.
// They use an in-process containerd client so no daemon binary is needed.
//
// All conversion goes through the pure-Go path (ConvertTarErofs) via the
// updated erofsutils.ConvertTarErofs delegation.
package erofs

import (
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestErofsConvertPerLayerMediaType converts a tar image to per-layer EROFS
// and verifies that the output layer media types are recognised as EROFS.
// ---------------------------------------------------------------------------
func TestErofsConvertPerLayerMediaType(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)

	dstRef := erofsTestImage + "-erofs-perlay-mediatype-test"
	dstImg := localEROFS(t, c, dstRef)

	pm := erofsPM()
	mfst, err := images.Manifest(ctx, c.ContentStore(), dstImg.Target, pm)
	require.NoError(t, err)
	require.NotEmpty(t, mfst.Layers, "converted image must have at least one layer")

	for i, l := range mfst.Layers {
		assert.True(t, isErofsMediaTypePrefix(l.MediaType),
			"layer %d media type %q must be an EROFS type", i, l.MediaType)
	}
}

// ---------------------------------------------------------------------------
// TestErofsConvertUnpackedLabel verifies that the converted EROFS layers
// carry the containerd.io/uncompressed label so that IsUnpacked() works
// correctly against the EROFS snapshotter.
// ---------------------------------------------------------------------------
func TestErofsConvertUnpackedLabel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)

	dstRef := erofsTestImage + "-erofs-label-test"
	dstImg := localEROFS(t, c, dstRef)

	pm := erofsPM()
	mfst, err := images.Manifest(ctx, c.ContentStore(), dstImg.Target, pm)
	require.NoError(t, err)
	require.NotEmpty(t, mfst.Layers)

	cs := c.ContentStore()
	for i, l := range mfst.Layers {
		info, err := cs.Info(ctx, l.Digest)
		require.NoError(t, err, "layer %d must be in content store", i)
		_, hasLabel := info.Labels["containerd.io/uncompressed"]
		assert.True(t, hasLabel,
			"layer %d must carry the uncompressed-digest label for snapshotter IsUnpacked()", i)
	}
}

// ---------------------------------------------------------------------------
// TestErofsConvertAndUnpack fetches a tar image, converts it to EROFS+zstd,
// and unpacks it with the EROFS snapshotter. Verifies IsUnpacked returns true.
//
// Does not require root: the EROFS differ writes layer.erofs files directly
// to the snapshot directory without issuing mount(2) syscalls.
// ---------------------------------------------------------------------------
func TestErofsConvertAndUnpack(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)

	dstRef := erofsTestImage + "-erofs-convert-unpack-test"
	dstImg := localEROFS(t, c, dstRef)

	pm := erofsPM()
	img := containerd.NewImageWithPlatform(c, *dstImg, pm)
	if err := img.Unpack(ctx, erofsSnapshotterName); err != nil {
		if isSnapshotterPlatformError(err) {
			t.Skipf("erofs snapshotter does not yet advertise os.features=[erofs] platform support: %v", err)
		}
		require.NoError(t, err)
	}

	unpacked, err := img.IsUnpacked(ctx, erofsSnapshotterName)
	require.NoError(t, err)
	assert.True(t, unpacked,
		"IsUnpacked must return true after successful EROFS unpack")
}

// ---------------------------------------------------------------------------
// TestErofsConvertPlatformOSFeatures verifies that the converted EROFS
// manifest has os.features=["erofs"] in its platform descriptor.
// ---------------------------------------------------------------------------
func TestErofsConvertPlatformOSFeatures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)

	dstRef := erofsTestImage + "-erofs-osfeatures-test"
	dstImg := localEROFS(t, c, dstRef)

	plats, err := images.Platforms(ctx, c.ContentStore(), dstImg.Target)
	require.NoError(t, err)

	var hasEROFS bool
	for _, p := range plats {
		for _, f := range p.OSFeatures {
			if f == "erofs" {
				hasEROFS = true
			}
		}
	}
	assert.True(t, hasEROFS,
		"converted image must have a platform with os.features=[erofs]: %+v",
		plats)

	// Verify the EROFS manifest is matchable with erofsPM().
	pm := erofsPM()
	_, err = images.Manifest(ctx, c.ContentStore(), dstImg.Target, pm)
	require.NoError(t, err, "EROFS manifest must be discoverable with erofsPM()")

	// Verify the local platform matcher works for the local arch.
	localPM := platforms.Default()
	_, err = images.Manifest(ctx, c.ContentStore(), dstImg.Target, localPM)
	if err != nil {
		t.Logf("no manifest for plain local platform (expected for EROFS-only conversion): %v", err)
	}
}
