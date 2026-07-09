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

// load_test.go contains tests for loading (import) of OCI tar archives into
// the EROFS snapshotter. The flow mirrors a docker-load / ctr images import
// workflow: fetch an image, export it to a tar archive, delete it, then
// re-import from the archive and unpack with the EROFS snapshotter.
//
// Tests use an in-process containerd client (see inproc_test.go) so they run
// on Linux, macOS, and Windows without a pre-installed daemon binary.
package erofs

import (
	"os"
	"path/filepath"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/archive"
	intimages "github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/platforms"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestImageLoad fetches the Alpine image, exports it as an OCI tar archive,
// deletes the original image, re-imports from the archive, unpacks with the
// EROFS snapshotter, and verifies the snapshot via fsview.FSMounts.
// ---------------------------------------------------------------------------
func TestImageLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping image load test in short mode")
	}

	c := newTestClient(t)
	ref := intimages.Get(intimages.Alpine)

	ctx, cancel := testContext(t)
	defer cancel()

	// ── Step 1: fetch the image into the content store ─────────────────────
	_, err := c.Fetch(ctx, ref,
		containerd.WithPlatformMatcher(platforms.Default()),
	)
	if err != nil {
		if isNetworkError(err) {
			t.Skipf("image %s not reachable (network error): %v", ref, err)
		}
		require.NoError(t, err, "fetch %s", ref)
	}
	t.Cleanup(func() {
		ctx2, cancel2 := testContext(t)
		defer cancel2()
		_ = c.ImageService().Delete(ctx2, ref, images.SynchronousDelete())
	})

	// ── Step 2: export to a temp tar file ──────────────────────────────────
	tarPath := filepath.Join(t.TempDir(), "image.tar")
	f, err := os.Create(tarPath)
	require.NoError(t, err, "create temp tar file")
	defer f.Close()

	require.NoError(t, c.Export(ctx, f,
		archive.WithPlatform(platforms.Default()),
		archive.WithImage(c.ImageService(), ref),
	), "export %s to tar", ref)

	// ── Step 3: delete the original image ──────────────────────────────────
	require.NoError(t,
		c.ImageService().Delete(ctx, ref, images.SynchronousDelete()),
		"delete original image before import",
	)

	// ── Step 4: seek to start and re-import ────────────────────────────────
	_, err = f.Seek(0, 0)
	require.NoError(t, err, "seek tar file to start")

	imgrecs, err := c.Import(ctx, f,
		containerd.WithImageRefTranslator(archive.AddRefPrefix("erofs-load-test")),
	)
	require.NoError(t, err, "import from tar archive")
	require.NotEmpty(t, imgrecs, "import must produce at least one image record")

	// ── Step 5: unpack and verify each imported image ──────────────────────
	for _, rec := range imgrecs {
		img := containerd.NewImage(c, rec)

		t.Logf("unpacking imported image %s (%s)", rec.Name, rec.Target.Digest)
		require.NoError(t,
			img.Unpack(ctx, erofsSnapshotterName),
			"unpack imported image %s with erofs snapshotter", rec.Name,
		)

		unpacked, err := img.IsUnpacked(ctx, erofsSnapshotterName)
		require.NoError(t, err, "IsUnpacked for %s", rec.Name)
		require.True(t, unpacked, "imported image %s must report as unpacked", rec.Name)

		_, view := mountAndView(t, c, rec)
		verifyView(t, view, alpinePresent, nil)

		t.Cleanup(func() {
			ctx2, cancel2 := testContext(t)
			defer cancel2()
			_ = c.ImageService().Delete(ctx2, rec.Name, images.SynchronousDelete())
		})
	}
}
