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

// exec_linux_test.go contains tests that check the EROFS mounts the
// snapshotter returns for an unpacked image: the layer blobs they name and
// their options. Nothing is mounted, so the tests need neither root nor
// kernel EROFS support.
package erofs

import (
	"os"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfEROFSMountUnavailable skips t in short mode and when the EROFS
// snapshotter is not registered in the daemon.
func skipIfEROFSMountUnavailable(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("short mode: skipping mount tests")
	}

	c := newTestClient(t)
	defer c.Close()

	ctx, cancel := testContext(t)
	defer cancel()
	_, err := c.GetSnapshotterCapabilities(ctx, erofsSnapshotterName)
	if err != nil {
		t.Skipf("erofs snapshotter not available: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestErofsExecLayerFileIntegrity verifies that the layer.erofs file produced
// by the Go conversion path has a valid EROFS superblock magic at offset 1024.
//
// It reads the source path from the mounts of a view of each layer, opens the
// file directly, and checks the magic bytes.
// ---------------------------------------------------------------------------
func TestErofsExecLayerFileIntegrity(t *testing.T) {
	skipIfEROFSMountUnavailable(t)

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)
	defer c.Close()

	dstRef := erofsTestImage + "-erofs-integrity-exec-test"
	dstImg := localEROFS(t, c, dstRef)

	pm := erofsPM()
	img := containerd.NewImageWithPlatform(c, *dstImg, pm)
	require.NoError(t, img.Unpack(ctx, erofsSnapshotterName))

	mfst, err := images.Manifest(ctx, c.ContentStore(), dstImg.Target, pm)
	require.NoError(t, err)

	for _, id := range chainIDs(t, c, mfst.Layers) {
		var checked int
		for _, m := range viewMounts(t, c, id) {
			if m.Type == "erofs" {
				checkEROFSSuperblock(t, m.Source)
				checked++
			}
		}
		assert.NotZero(t, checked, "view of %s must have an erofs mount", id)
	}
}

// ---------------------------------------------------------------------------
// TestErofsExecMountReadonly verifies that the EROFS mounts of a view of each
// layer include the "ro" (read-only) option.
// ---------------------------------------------------------------------------
func TestErofsExecMountReadonly(t *testing.T) {
	skipIfEROFSMountUnavailable(t)

	ctx, cancel := testContext(t)
	defer cancel()

	c := newTestClient(t)
	defer c.Close()

	dstRef := erofsTestImage + "-erofs-readonly-spec-test"
	dstImg := localEROFS(t, c, dstRef)

	pm := erofsPM()
	img := containerd.NewImageWithPlatform(c, *dstImg, pm)
	require.NoError(t, img.Unpack(ctx, erofsSnapshotterName))

	mfst, err := images.Manifest(ctx, c.ContentStore(), dstImg.Target, pm)
	require.NoError(t, err)

	for _, id := range chainIDs(t, c, mfst.Layers) {
		var checked int
		for _, m := range viewMounts(t, c, id) {
			if m.Type == "erofs" {
				checked++
				var hasRO bool
				for _, opt := range m.Options {
					if opt == "ro" {
						hasRO = true
						break
					}
				}
				assert.True(t, hasRO,
					"EROFS mount spec for snapshot %s must include 'ro' option", id)
			}
		}
		assert.NotZero(t, checked, "view of %s must have an erofs mount", id)
	}
}

// ---------------------------------------------------------------------------
// checkEROFSSuperblock reads bytes 1024–1027 from path and asserts the EROFS
// magic 0xE0F5E1E2 (little-endian: bytes E2 E1 F5 E0).
// ---------------------------------------------------------------------------
func checkEROFSSuperblock(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err, "open layer blob")
	defer f.Close()

	var magic [4]byte
	_, err = f.ReadAt(magic[:], 1024)
	require.NoError(t, err, "read superblock from %q", path)
	assert.Equal(t, [4]byte{0xE2, 0xE1, 0xF5, 0xE0}, magic,
		"file %q must have EROFS superblock magic at offset 1024", path)
}

// viewMounts returns the mounts of a temporary view of the committed snapshot
// id, removed when the test finishes. A committed snapshot has no mounts of its
// own: Mounts only answers for active and view snapshots.
func viewMounts(t *testing.T, c *containerd.Client, id string) []mount.Mount {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()

	sn := c.SnapshotService(erofsSnapshotterName)
	key := t.Name() + "-view-" + id
	mounts, err := sn.View(ctx, key, id)
	require.NoError(t, err, "create view of %s", id)
	t.Cleanup(func() {
		ctx, cancel := testContext(t)
		defer cancel()
		_ = sn.Remove(ctx, key)
	})
	return mounts
}
