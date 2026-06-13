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

// verify_test.go provides helpers for verifying unpacked EROFS snapshots
// via fsview.FSMounts. No external daemon or root privilege is required.
package erofs

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/fsview"
	"github.com/containerd/platforms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mountAndView creates a KindView snapshot from the last chain ID of the
// unpacked image and opens it as an fsview.View. The caller must call
// cleanup when done.
//
// It uses the EROFS snapshotter and relies on the fsview EROFS handler
// (registered in inproc_test.go via blank import) so that erofs-type mounts
// are opened directly from their source files without requiring mount(2).
func mountAndView(t *testing.T, c *containerd.Client, img images.Image) ([]mount.Mount, fsview.View) {
	t.Helper()

	ctx, cancel := testContext(t)
	defer cancel()

	pm := platforms.Default()
	mfst, err := images.Manifest(ctx, c.ContentStore(), img.Target, pm)
	require.NoError(t, err, "get image manifest")
	require.NotEmpty(t, mfst.Layers, "image must have at least one layer")

	ids := chainIDs(t, c, mfst.Layers)
	lastID := ids[len(ids)-1]

	sn := c.SnapshotService(erofsSnapshotterName)

	// Committed snapshots require a View (KindView) to obtain mounts.
	// Use a unique key per test invocation to avoid collisions.
	viewKey := fmt.Sprintf("%s-view-%s", t.Name(), lastID)
	mounts, err := sn.View(ctx, viewKey, lastID,
		snapshots.WithLabels(map[string]string{"containerd.io/gc.root": "1"}),
	)
	require.NoError(t, err, "create view snapshot for %s", lastID)
	require.NotEmpty(t, mounts, "view snapshot must have at least one mount")

	t.Cleanup(func() {
		ctx2, cancel2 := testContext(t)
		defer cancel2()
		_ = sn.Remove(ctx2, viewKey)
	})

	view, err := fsview.FSMounts(mounts)
	require.NoError(t, err, "open snapshot mounts as fsview")
	require.NotNil(t, view, "fsview must not be nil")

	t.Cleanup(func() { view.Close() })
	return mounts, view
}

// verifyView asserts that every path in present exists in view (and is not a
// whiteout), and that every path in absent either does not exist or is a
// whiteout entry. Paths use forward-slash separators.
func verifyView(t *testing.T, view fsview.View, present, absent []string) {
	t.Helper()

	for _, p := range present {
		fi, err := fs.Stat(view, p)
		if assert.NoError(t, err, "path %q must exist in snapshot", p) {
			assert.False(t, fsview.IsWhiteout(fi),
				"path %q must not be a whiteout", p)
		}
	}

	for _, p := range absent {
		fi, err := fs.Stat(view, p)
		if errors.Is(err, fs.ErrNotExist) {
			// Cleanly absent — correct.
			continue
		}
		// If the file exists it must be a whiteout (char dev with rdev==0).
		if assert.NoError(t, err, "stat %q", p) {
			assert.True(t, fsview.IsWhiteout(fi),
				"path %q must be absent or a whiteout, but found a regular entry", p)
		}
	}
}

// ── Expected-path tables ──────────────────────────────────────────────────

// alpinePresent are paths expected to exist in a fully-unpacked Alpine image.
var alpinePresent = []string{
	"etc/alpine-release",
	"etc/os-release",
	"bin/busybox",
}

// busyboxPresent are paths expected to exist in a fully-unpacked BusyBox image.
var busyboxPresent = []string{
	"bin/busybox",
}

// whiteoutPresent are paths that must exist in the final view of the
// whiteout-test image (after all layers have been applied).
//
// ghcr.io/containerd/whiteout-test:1.0 is a five-layer image built on top of
// busybox:
//   - layer 1: busybox filesystem
//   - layer 2: adds /file-to-delete
//   - layer 3: whiteouts /file-to-delete  (.wh.file-to-delete)
//   - layer 4: adds /dir-to-delete/foo
//   - layer 5: whiteouts /dir-to-delete   (.wh.dir-to-delete)
//
// After applying all layers the final merged view must contain the busybox
// filesystem but neither /file-to-delete nor /dir-to-delete.
var whiteoutPresent = []string{
	"bin/busybox",
}

// whiteoutAbsent are paths that must be absent (whited-out) in the final view.
var whiteoutAbsent = []string{
	"file-to-delete",
	"dir-to-delete",
}
