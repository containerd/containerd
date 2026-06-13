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

// pull_test.go contains integration tests for pulling and unpacking standard
// OCI tar images using the EROFS snapshotter and verifying the resulting
// snapshot via fsview.FSMounts — no kernel mount or root privilege required.
//
// Tests use an in-process containerd client (see inproc_test.go) so they run
// on Linux, macOS, and Windows without a pre-installed daemon binary.
package erofs

import (
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	intimages "github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/platforms"
	"github.com/stretchr/testify/require"
)

// pullAndUnpack fetches ref and unpacks it with the EROFS snapshotter.
// Returns the pulled image. Registers a cleanup that deletes the image.
func pullAndUnpack(t *testing.T, c *containerd.Client, ref string) images.Image {
	t.Helper()

	ctx, cancel := testContext(t)
	defer cancel()

	img, err := c.Pull(ctx, ref,
		containerd.WithPlatformMatcher(platforms.Default()),
	)
	if err != nil {
		if isNetworkError(err) {
			t.Skipf("image %s not reachable (network error): %v", ref, err)
		}
		require.NoError(t, err, "pull %s", ref)
	}

	t.Cleanup(func() {
		ctx2, cancel2 := testContext(t)
		defer cancel2()
		_ = c.ImageService().Delete(ctx2, ref, images.SynchronousDelete())
	})

	require.NoError(t,
		img.Unpack(ctx, erofsSnapshotterName),
		"unpack %s with erofs snapshotter", ref,
	)

	unpacked, err := img.IsUnpacked(ctx, erofsSnapshotterName)
	require.NoError(t, err, "IsUnpacked for %s", ref)
	require.True(t, unpacked, "image %s must report as unpacked after Unpack", ref)

	return img.Metadata()
}

// ---------------------------------------------------------------------------
// TestPullAlpine pulls ghcr.io/containerd/alpine:3.14.0, unpacks it with the
// EROFS snapshotter, and verifies a set of known paths via fsview.FSMounts.
// ---------------------------------------------------------------------------
func TestPullAlpine(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pull test in short mode")
	}

	c := newTestClient(t)
	ref := intimages.Get(intimages.Alpine)

	img := pullAndUnpack(t, c, ref)
	_, view := mountAndView(t, c, img)
	verifyView(t, view, alpinePresent, nil)
}

// ---------------------------------------------------------------------------
// TestPullBusyBox pulls ghcr.io/containerd/busybox:1.36, unpacks it with the
// EROFS snapshotter, and verifies a set of known paths via fsview.FSMounts.
// ---------------------------------------------------------------------------
func TestPullBusyBox(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pull test in short mode")
	}

	c := newTestClient(t)
	ref := intimages.Get(intimages.BusyBox)

	img := pullAndUnpack(t, c, ref)
	_, view := mountAndView(t, c, img)
	verifyView(t, view, busyboxPresent, nil)
}

// ---------------------------------------------------------------------------
// TestPullWhiteout pulls ghcr.io/containerd/whiteout-test:1.0, unpacks it
// with the EROFS snapshotter, and verifies that whited-out files are absent
// and surviving files are present in the final snapshot view.
// ---------------------------------------------------------------------------
func TestPullWhiteout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pull test in short mode")
	}

	c := newTestClient(t)
	ref := intimages.Get(intimages.Whiteout)

	img := pullAndUnpack(t, c, ref)
	_, view := mountAndView(t, c, img)
	verifyView(t, view, whiteoutPresent, whiteoutAbsent)
}
