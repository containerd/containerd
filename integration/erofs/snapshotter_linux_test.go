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

// snapshotter_linux_test.go contains integration tests for the EROFS
// snapshotter that exercise the Prepare/Apply/Commit lifecycle.
//
// # Root requirements
//
// These tests do NOT require root. The EROFS Prepare/Apply/Commit pipeline
// only writes EROFS image files (layer.erofs) and bolt records; it does not
// issue mount(2) syscalls. Only tests in exec_linux_test.go actually mount
// EROFS images and therefore need root.
//
// Tests use an in-process containerd client so no daemon binary is needed.
//
// # What is tested
//
//   - After converting and unpacking an EROFS image, every layer snapshot is
//     in KindCommitted state (Stat() is a boltdb read).
//   - Mounts() via a KindView snapshot returns non-empty descriptors with
//     a non-empty Source path (metadata read, no mount(2)).
package erofs

import (
	"fmt"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestErofsSnapshotterCommitted converts and unpacks a local EROFS image,
// then verifies that every resulting snapshot is in KindCommitted state.
// ---------------------------------------------------------------------------
func TestErofsSnapshotterCommitted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)

	dstRef := erofsTestImage + "-erofs-snap-committed-test"
	dstImg := localEROFS(t, c, dstRef)

	pm := erofsPM()
	img := containerd.NewImageWithPlatform(c, *dstImg, pm)
	if err := img.Unpack(ctx, erofsSnapshotterName); err != nil {
		if isSnapshotterPlatformError(err) {
			t.Skipf("erofs snapshotter does not yet advertise os.features=[erofs] platform support: %v", err)
		}
		require.NoError(t, err)
	}

	mfst, err := images.Manifest(ctx, c.ContentStore(), dstImg.Target, pm)
	require.NoError(t, err)

	sn := c.SnapshotService(erofsSnapshotterName)
	for _, id := range chainIDs(t, c, mfst.Layers) {
		info, err := sn.Stat(ctx, id)
		require.NoError(t, err, "snapshot %s must exist after unpack", id)
		assert.Equal(t, snapshots.KindCommitted, info.Kind,
			"snapshot %s must be in KindCommitted state", id)
	}
}

// ---------------------------------------------------------------------------
// TestErofsSnapshotterMountsSpec verifies that a KindView snapshot over a
// committed EROFS snapshot returns non-empty mount descriptors with populated
// Source paths. This is a metadata-only operation; no mount(2) is issued.
// ---------------------------------------------------------------------------
func TestErofsSnapshotterMountsSpec(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)

	dstRef := erofsTestImage + "-erofs-snap-mountspec-test"
	dstImg := localEROFS(t, c, dstRef)

	pm := erofsPM()
	img := containerd.NewImageWithPlatform(c, *dstImg, pm)
	if err := img.Unpack(ctx, erofsSnapshotterName); err != nil {
		if isSnapshotterPlatformError(err) {
			t.Skipf("erofs snapshotter does not yet advertise os.features=[erofs] platform support: %v", err)
		}
		require.NoError(t, err)
	}

	mfst, err := images.Manifest(ctx, c.ContentStore(), dstImg.Target, pm)
	require.NoError(t, err)

	sn := c.SnapshotService(erofsSnapshotterName)
	for i, id := range chainIDs(t, c, mfst.Layers) {
		// Committed snapshots require a KindView to get mounts.
		viewKey := fmt.Sprintf("%s-view-%d", t.Name(), i)
		mounts, err := sn.View(ctx, viewKey, id)
		require.NoError(t, err, "View() must succeed for committed snapshot %s", id)
		t.Cleanup(func() {
			ctx2, cancel2 := testContext(t)
			defer cancel2()
			_ = sn.Remove(ctx2, viewKey)
		})
		assert.NotEmpty(t, mounts, "View() must return at least one mount descriptor")
		for _, m := range mounts {
			assert.NotEmpty(t, m.Source, "mount descriptor source path must be non-empty")
			t.Logf("snapshot %s: type=%s source=%s", id, m.Type, m.Source)
		}
	}
}

// ---------------------------------------------------------------------------
// TestErofsSnapshotterIsUnpacked verifies that IsUnpacked returns true
// after unpacking with the EROFS snapshotter.
// ---------------------------------------------------------------------------
func TestErofsSnapshotterIsUnpacked(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)

	dstRef := erofsTestImage + "-erofs-snap-isunpacked-test"
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
	assert.True(t, unpacked, "IsUnpacked must return true after EROFS unpack")
}
