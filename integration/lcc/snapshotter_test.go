//go:build linux && integration

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

package lcc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
)

// TestLCCImageLifecycle is the primary end-to-end test. It exercises the full
// pipeline:
//
//  1. Create a minimal package blob (static binary that prints "hello").
//  2. Build an OCI image tarball with pkg2oci.
//  3. Start containerd with the built-in overlay snapshotter.
//  4. Import the image via ctr (triggers unpack through the snapshotter).
//  5. Verify committed snapshots and a populated LCC cache directory.
//  6. Run a container and verify the binary output.
//  7. Prune and verify all snapshots and LCC cache dirs are removed.
func TestLCCImageLifecycle(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root (overlayfs + containerd)")
	}
	checkBinaries(t, "containerd", "ctr", "pkg2oci")

	env := setupEnv(t)
	const ref = "localhost/test/myapp:v1"
	tarPath := filepath.Join(env.tmp, "myapp.tar")

	blobPath := makePrintBlob(t, env.tmp, "hello-1.0.tar.zst", "usr/bin/hello", "hello\n")

	runCmd(t, env.ctx, "pkg2oci",
		"--output", tarPath,
		"--package", blobPath,
		ref,
	)
	t.Logf("pkg2oci: wrote %s", tarPath)

	ctrImport(t, env, tarPath)

	snapSvc := env.ctrdClient.SnapshotService("overlayfs")
	var committed int
	if err := snapSvc.Walk(env.ctx, func(ctx context.Context, info snapshots.Info) error {
		t.Logf("snapshot: name=%s kind=%v", info.Name, info.Kind)
		if info.Kind == snapshots.KindCommitted {
			committed++
		}
		return nil
	}); err != nil {
		t.Fatalf("Walk snapshots: %v", err)
	}
	if committed == 0 {
		t.Error("expected at least one committed snapshot after unpack")
	}

	cacheDir := env.cacheDir
	blobCount := countCachedBlobs(t, cacheDir)
	if blobCount == 0 {
		t.Error("expected at least one LCC cache directory")
	}
	t.Logf("committed snapshots: %d  LCC cache dirs: %d", committed, blobCount)

	out := ctrRun(t, env, ref, "hello-run", "/usr/bin/hello")
	t.Logf("container output: %q", out)
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("want %q, got %q", "hello", strings.TrimSpace(out))
	}

	pruneAll(t, env, ref)
}

// TestLCCMultiPackageImage verifies that a LCC image containing two packages
// produces two LCC cache directories and two committed snapshots, that both
// binaries run correctly, and that pruning leaves a clean state.
func TestLCCMultiPackageImage(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root (overlayfs + containerd)")
	}
	checkBinaries(t, "containerd", "ctr", "pkg2oci")

	env := setupEnv(t)
	const ref = "localhost/test/multi:v1"
	tarPath := filepath.Join(env.tmp, "multi.tar")

	blob1 := makePrintBlob(t, env.tmp, "hello-1.0.tar.zst", "usr/bin/hello", "hello\n")
	blob2 := makePrintBlob(t, env.tmp, "world-1.0.tar.zst", "usr/bin/world", "world\n")

	runCmd(t, env.ctx, "pkg2oci",
		"--output", tarPath,
		"--package", blob1,
		"--package", blob2,
		ref,
	)
	t.Logf("pkg2oci: wrote %s", tarPath)

	ctrImport(t, env, tarPath)

	cacheDir := env.cacheDir
	blobCount := countCachedBlobs(t, cacheDir)
	if blobCount != 2 {
		t.Errorf("want 2 LCC cache dirs, got %d", blobCount)
	}

	snapSvc := env.ctrdClient.SnapshotService("overlayfs")
	var committed int
	if err := snapSvc.Walk(env.ctx, func(ctx context.Context, info snapshots.Info) error {
		t.Logf("snapshot: name=%s kind=%v parent=%s", info.Name, info.Kind, info.Parent)
		if info.Kind == snapshots.KindCommitted {
			committed++
		}
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if committed != 2 {
		t.Errorf("want 2 committed snapshots, got %d", committed)
	}
	t.Logf("LCC cache dirs: %d  committed snapshots: %d", blobCount, committed)

	out1 := ctrRun(t, env, ref, "hello-run", "/usr/bin/hello")
	if strings.TrimSpace(out1) != "hello" {
		t.Errorf("hello: want %q, got %q", "hello", strings.TrimSpace(out1))
	}
	out2 := ctrRun(t, env, ref, "world-run", "/usr/bin/world")
	if strings.TrimSpace(out2) != "world" {
		t.Errorf("world: want %q, got %q", "world", strings.TrimSpace(out2))
	}

	pruneAll(t, env, ref)
}

// TestLCCDuplicateLayer verifies that an image whose layer list contains the
// same blob at two positions can be imported and run correctly. The sequence
// number ensures each occurrence of the repeated diffID maps to a distinct cache
// directory, satisfying the OverlayFS constraint that all lower_dir entries must
// be distinct.
//
// Image layout: [pkgA, pkgB, pkgA] — pkgA appears at positions 0 and 2.
// Expected outcome: three cache directories (pkgA.seq0, pkgB.seq0, pkgA.seq1),
// all accessible and distinct, and the container can run both binaries.
func TestLCCDuplicateLayer(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root (overlayfs + containerd)")
	}
	checkBinaries(t, "containerd", "ctr", "pkg2oci")

	env := setupEnv(t)
	const ref = "localhost/test/dup:v1"
	tarPath := filepath.Join(env.tmp, "dup.tar")

	blobA := makePrintBlob(t, env.tmp, "pkgA.tar.zst", "usr/bin/pkgA", "pkgA\n")
	blobB := makePrintBlob(t, env.tmp, "pkgB.tar.zst", "usr/bin/pkgB", "pkgB\n")

	// Pass blobA twice so the image has layers [A, B, A].
	runCmd(t, env.ctx, "pkg2oci",
		"--output", tarPath,
		"--package", blobA,
		"--package", blobB,
		"--package", blobA,
		ref,
	)

	ctrImport(t, env, tarPath)

	// Three layers → three cache dirs: pkgA.seq=0, pkgB.seq=0, pkgA.seq=1.
	cacheDir := env.cacheDir
	blobCount := countCachedBlobs(t, cacheDir)
	if blobCount != 3 {
		t.Errorf("want 3 LCC cache dirs (pkgA.seq0, pkgB.seq0, pkgA.seq1), got %d", blobCount)
	}

	// Verify all three cache dirs are distinct (OverlayFS correctness).
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("ReadDir cache: %v", err)
	}
	seen := map[string]struct{}{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, dup := seen[e.Name()]; dup {
			t.Errorf("duplicate cache dir name: %s", e.Name())
		}
		seen[e.Name()] = struct{}{}
	}

	outA := ctrRun(t, env, ref, "dup-run-a", "/usr/bin/pkgA")
	if strings.TrimSpace(outA) != "pkgA" {
		t.Errorf("pkgA: want %q, got %q", "pkgA", strings.TrimSpace(outA))
	}
	outB := ctrRun(t, env, ref, "dup-run-b", "/usr/bin/pkgB")
	if strings.TrimSpace(outB) != "pkgB" {
		t.Errorf("pkgB: want %q, got %q", "pkgB", strings.TrimSpace(outB))
	}

	pruneAll(t, env, ref)
}

// TestLCCLayerCacheIndependence demonstrates the core CAS property: each layer
// blob is cached independently by content digest. Evicting a single blob does
// not invalidate cache entries for any other layer. On the next import, only the
// missing blob is re-extracted; layers above and below it are served from the
// local cache.
//
// Test setup:
//
//	Image A: [pkg1, pkg2, pkg3, pkg4]   — four layers
//	Image B: [pkg1, pkg3, pkg4]          — same packages except pkg2
//
// Import B first to seed pkg1, pkg3, pkg4. Import A — only pkg2 is extracted.
// Remove A — GC evicts pkg2 (referenced only by A's snapshots); pkg1, pkg3,
// pkg4 survive because B's snapshots still reference them. Re-import A — only
// pkg2 is re-extracted.
func TestLCCLayerCacheIndependence(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root (overlayfs + containerd)")
	}
	checkBinaries(t, "containerd", "ctr", "pkg2oci")

	env := setupEnv(t)
	const refA = "localhost/test/a:v1"
	const refB = "localhost/test/b:v1"
	tarA := filepath.Join(env.tmp, "a.tar")
	tarB := filepath.Join(env.tmp, "b.tar")
	cacheDir := env.cacheDir
	snapSvc := env.ctrdClient.SnapshotService("overlayfs")

	blob1 := makePrintBlob(t, env.tmp, "pkg1.tar.zst", "usr/bin/pkg1", "pkg1\n")
	blob2 := makePrintBlob(t, env.tmp, "pkg2.tar.zst", "usr/bin/pkg2", "pkg2\n")
	blob3 := makePrintBlob(t, env.tmp, "pkg3.tar.zst", "usr/bin/pkg3", "pkg3\n")
	blob4 := makePrintBlob(t, env.tmp, "pkg4.tar.zst", "usr/bin/pkg4", "pkg4\n")

	runCmd(t, env.ctx, "pkg2oci", "--output", tarA,
		"--package", blob1,
		"--package", blob2,
		"--package", blob3,
		"--package", blob4,
		refA,
	)
	runCmd(t, env.ctx, "pkg2oci", "--output", tarB,
		"--package", blob1,
		"--package", blob3,
		"--package", blob4,
		refB,
	)

	// Import B: seeds pkg1, pkg3, pkg4.
	ctrImport(t, env, tarB)
	if countCachedBlobs(t, cacheDir) != 3 {
		t.Fatalf("want 3 LCC cache dirs after importing B, got %d", countCachedBlobs(t, cacheDir))
	}

	bSnapshotCount := 0
	_ = snapSvc.Walk(env.ctx, func(_ context.Context, _ snapshots.Info) error {
		bSnapshotCount++
		return nil
	})

	// Import A: only pkg2 is extracted; pkg1, pkg3, pkg4 served from cache.
	ctrImport(t, env, tarA)
	if countCachedBlobs(t, cacheDir) != 4 {
		t.Fatalf("want 4 LCC cache dirs after importing A, got %d", countCachedBlobs(t, cacheDir))
	}

	// Record inodes of all four LCC cache dirs.
	inodesBefore := blobDirInodes(t, cacheDir)

	// Remove image A. GC removes A's snapshots, then Cleanup() evicts pkg2
	// (no longer referenced). pkg1, pkg3, pkg4 survive (still in B's chain).
	runCmd(t, env.ctx, "ctr",
		"--address", env.ctrdSock, "--namespace", "default",
		"images", "rm", refA,
	)

	var blobCount, snapCount int
	for attempt := range pruneMaxAttempts {
		time.Sleep(pruneInterval)
		blobCount = countCachedBlobs(t, cacheDir)
		snapCount = 0
		_ = snapSvc.Walk(env.ctx, func(_ context.Context, _ snapshots.Info) error {
			snapCount++
			return nil
		})
		if blobCount == 3 && snapCount == bSnapshotCount {
			break
		}
		t.Logf("attempt %d/%d: %d LCC cache dirs, %d snapshots (want 3 dirs, %d snapshots)",
			attempt+1, pruneMaxAttempts, blobCount, snapCount, bSnapshotCount)
	}
	if blobCount != 3 {
		t.Fatalf("want 3 LCC cache dirs after removing A (pkg2 evicted), got %d", blobCount)
	}
	if snapCount != bSnapshotCount {
		t.Fatalf("want %d snapshots after removing A, got %d", bSnapshotCount, snapCount)
	}
	t.Logf("after removing A: %d LCC cache dirs (pkg1, pkg3, pkg4); pkg2 evicted", blobCount)

	// Re-import A. The snapshotter finds pkg1, pkg3, pkg4 in the local cache
	// and sets LabelSkipApply; only pkg2 triggers a fresh extraction.
	ctrImport(t, env, tarA)

	if countCachedBlobs(t, cacheDir) != 4 {
		t.Errorf("want 4 LCC cache dirs after re-importing A, got %d", countCachedBlobs(t, cacheDir))
	}

	inodesAfter := blobDirInodes(t, cacheDir)

	var refetched, cached int
	for path, ino := range inodesAfter {
		if prev, ok := inodesBefore[path]; ok && ino == prev {
			cached++
			t.Logf("cache hit:  %s", filepath.Base(filepath.Dir(filepath.Dir(path))))
		} else {
			refetched++
			t.Logf("re-extracted: %s", filepath.Base(filepath.Dir(filepath.Dir(path))))
		}
	}

	if refetched != 1 {
		t.Errorf("want 1 re-extracted blob (pkg2), got %d (cached=%d)", refetched, cached)
	}
	if cached != 3 {
		t.Errorf("want 3 cache hits (pkg1, pkg3, pkg4), got %d (re-extracted=%d)", cached, refetched)
	}

	out := ctrRun(t, env, refA, "independence-test", "/usr/bin/pkg3")
	if strings.TrimSpace(out) != "pkg3" {
		t.Errorf("container: want %q, got %q", "pkg3", strings.TrimSpace(out))
	}
}
