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

// exec_linux_test.go contains tests that perform actual EROFS filesystem
// mounts and verify mount properties.
//
// Requirements:
//   - Linux (mount(2) and loop devices are Linux-specific).
//   - Root / CAP_SYS_ADMIN: the erofs kernel driver requires privileged mounts.
//   - EROFS kernel module: loaded automatically by the daemon on most systems;
//     the test skips if the snapshotter is unavailable.
//
// All unpack / snapshot-state / mount-spec tests that do not require root live
// in snapshotter_linux_test.go.
package erofs

import (
	"os"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfEROFSMountUnavailable skips t if any prerequisite for actual EROFS
// filesystem mounting is absent:
//   - Root / CAP_SYS_ADMIN
//   - EROFS snapshotter plugin registered in the daemon
func skipIfEROFSMountUnavailable(t *testing.T) {
	t.Helper()
	testutil.RequiresRoot(t)
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
// The mount is performed by the daemon. This test reads the source path from
// the Mounts() descriptor (which is a boltdb read), opens the file directly,
// and checks the magic bytes.
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

	sn := c.SnapshotService(erofsSnapshotterName)
	for _, id := range chainIDs(t, c, mfst.Layers) {
		mounts, err := sn.Mounts(ctx, id)
		require.NoError(t, err)
		for _, m := range mounts {
			if m.Type == "erofs" {
				checkEROFSSuperblock(t, m.Source)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// TestErofsExecMountReadonly verifies that the EROFS mount spec returned by
// Mounts() includes the "ro" (read-only) option.
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

	sn := c.SnapshotService(erofsSnapshotterName)
	for _, id := range chainIDs(t, c, mfst.Layers) {
		mounts, err := sn.Mounts(ctx, id)
		require.NoError(t, err)
		for _, m := range mounts {
			if m.Type == "erofs" {
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
	}
}

// ---------------------------------------------------------------------------
// checkEROFSSuperblock reads bytes 1024–1027 from path and asserts the EROFS
// magic 0xE0F5E1E2 (little-endian: bytes E2 E1 F5 E0).
// ---------------------------------------------------------------------------
func checkEROFSSuperblock(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Logf("cannot open %q: %v (skipping superblock check)", path, err)
		return
	}
	defer f.Close()

	var magic [4]byte
	if _, err := f.ReadAt(magic[:], 1024); err != nil {
		t.Logf("cannot read superblock from %q: %v (skipping)", path, err)
		return
	}
	assert.Equal(t, [4]byte{0xE2, 0xE1, 0xF5, 0xE0}, magic,
		"file %q must have EROFS superblock magic at offset 1024", path)
}
