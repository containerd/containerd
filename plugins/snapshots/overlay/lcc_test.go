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

package overlay

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"
	"github.com/containerd/containerd/v2/pkg/rootfs"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

// mockApplier is a diff.Applier for tests. fn is called each time Apply is
// invoked. fn must not be nil; use a function that calls t.Fatal if Apply
// should never be called in a particular test.
type mockApplier struct {
	fn func()
}

func (m *mockApplier) Apply(_ context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	m.fn()
	return desc, nil
}

func TestOverlayLCC(t *testing.T) {
	testutil.RequiresRoot(t)
	optTestCases := map[string][]Opt{
		"WithLayerContentCache":                    {WithLayerContentCache},
		"WithLayerContentCache/AsynchronousRemove": {WithLayerContentCache, AsynchronousRemove},
		"WithLayerContentCache/WithRemapIDs":       {WithLayerContentCache, WithRemapIDs},
	}
	for optsName, opts := range optTestCases {
		t.Run(optsName, func(t *testing.T) {
			newSnapshotter := newSnapshotterWithOpts(opts...)
			t.Run("ContentPath", func(t *testing.T) {
				testLCCContentPath(t, newSnapshotter)
			})
			t.Run("ContentPath_InvalidDiffID", func(t *testing.T) {
				testLCCContentPath_InvalidDiffID(t, newSnapshotter)
			})
			t.Run("CacheMiss", func(t *testing.T) {
				testLCCCacheMiss(t, newSnapshotter)
			})
			t.Run("CacheHit", func(t *testing.T) {
				testLCCCacheHit(t, newSnapshotter)
			})
			t.Run("CacheHit_Commit", func(t *testing.T) {
				testLCCCacheHit_Commit(t, newSnapshotter)
			})
			t.Run("ConcurrentExtraction", func(t *testing.T) {
				testLCCConcurrentExtraction(t, newSnapshotter)
			})
			t.Run("Cleanup", func(t *testing.T) {
				testLCCCleanup(t, newSnapshotter)
			})
			t.Run("Cleanup_SharedEntry", func(t *testing.T) {
				testLCCCleanup_SharedEntry(t, newSnapshotter)
			})
			t.Run("OwnershipIsolation", func(t *testing.T) {
				testLCCOwnershipIsolation(t, newSnapshotter)
			})
			t.Run("NoOpWithoutDiffID", func(t *testing.T) {
				testLCCNoOpWithoutDiffID(t, newSnapshotter)
			})
			t.Run("ApplyLayerSkipsOnCacheHit", func(t *testing.T) {
				testLCCApplyLayerSkipsOnCacheHit(t, newSnapshotter)
			})
			t.Run("OrphanedStagingPreventsStaleHit", func(t *testing.T) {
				testLCCOrphanedStagingPreventsStaleHit(t, newSnapshotter)
			})
			t.Run("DuplicateLayer", func(t *testing.T) {
				testLCCDuplicateLayer(t, newSnapshotter)
			})
			t.Run("DuplicateLayer_CacheHit", func(t *testing.T) {
				testLCCDuplicateLayerCacheHit(t, newSnapshotter)
			})
			t.Run("CountDiffIDInChain", func(t *testing.T) {
				testLCCCountDiffIDInChain(t, newSnapshotter)
			})
		})
	}
}

// prepareAndCommitLCC runs the minimal Prepare → write file → Commit cycle for
// a layer-unpack snapshot (one that carries LabelSnapshotDiffID). parent is the
// name of the parent committed snapshot, or "" for a root snapshot. The diffID
// label is passed to both Prepare and Commit, mirroring the real unpacker.
func prepareAndCommitLCC(t *testing.T, ctx context.Context, sn *testSnapshotter, root, activeKey, committedName, parent, diffID string) {
	t.Helper()
	labels := snapshots.WithLabels(map[string]string{snapshots.LabelSnapshotDiffID: diffID})
	if _, err := sn.Prepare(ctx, activeKey, parent, labels); err != nil {
		t.Fatalf("Prepare %q: %v", activeKey, err)
	}
	fsDir := filepath.Join(getBasePath(ctx, sn, root, activeKey), "fs")
	if err := os.WriteFile(filepath.Join(fsDir, "file"), []byte("content"), 0644); err != nil {
		t.Fatalf("write into fs: %v", err)
	}
	if err := sn.Commit(ctx, committedName, activeKey, labels); err != nil {
		t.Fatalf("Commit %q: %v", committedName, err)
	}
}

// getCommittedBasePath returns the on-disk base directory for a committed
// snapshot. storage.GetSnapshot only works for active/view snapshots;
// storage.GetInfo works for all kinds.
func getCommittedBasePath(ctx context.Context, sn *testSnapshotter, root, key string) string {
	ctx, t, err := sn.ms.TransactionContext(ctx, false)
	if err != nil {
		panic(err)
	}
	defer t.Rollback()
	id, _, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		panic(err)
	}
	return filepath.Join(root, "snapshots", id)
}

// testLCCContentPath verifies the cache path layout:
// <root>/cache/<algo>.<hex>.<uid>.<gid>.<seq>
func testLCCContentPath(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	info := snapshots.Info{Labels: map[string]string{
		snapshots.LabelSnapshotDiffID: "sha256:abcdef1234abcdef1234abcdef1234abcdef1234abcdef1234abcdef12345678",
		labelSnapshotUID:              "0",
		labelSnapshotGID:              "0",
		labelSnapshotDiffIDSeq:        "0",
	}}
	got, err := sn.lcc.contentPath(info)
	if err != nil {
		t.Fatalf("lcc.contentPath: %v", err)
	}
	want := filepath.Join(root, "cache", "sha256.abcdef1234abcdef1234abcdef1234abcdef1234abcdef1234abcdef12345678.0.0.0")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func testLCCContentPath_InvalidDiffID(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	for _, tc := range []struct {
		name   string
		diffID string
	}{
		{"no colon", "nodice"},
		{"empty", ""},
		{"no algorithm", ":hex"},
		{"no hex", "algo:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := snapshots.Info{Labels: map[string]string{
				snapshots.LabelSnapshotDiffID: tc.diffID,
				labelSnapshotUID:              "0",
				labelSnapshotGID:              "0",
			}}
			if _, err := sn.lcc.contentPath(info); err == nil {
				t.Errorf("expected error for diffID %q, got nil", tc.diffID)
			}
		})
	}
}

// testLCCCacheMiss verifies the first-extraction path: fs/ is a real directory
// before commit (so the unpacker can write into it), LabelSkipApply is not set,
// and after commit the content is promoted to the cache and fs/ becomes a
// symlink to the cache entry.
func testLCCCacheMiss(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffID = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	const activeKey = "active-1"
	const name = "committed-1"

	labels := snapshots.WithLabels(map[string]string{snapshots.LabelSnapshotDiffID: diffID})
	if _, err := sn.Prepare(ctx, activeKey, "", labels); err != nil {
		t.Fatal(err)
	}

	// On a cache miss fs/ must be a real directory so the unpacker can extract into it.
	fsPath := filepath.Join(getBasePath(ctx, sn, root, activeKey), "fs")
	fi, err := os.Lstat(fsPath)
	if err != nil {
		t.Fatalf("Lstat fs: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("fs/ should be a real directory on cache miss, got symlink")
	}

	// LabelSkipApply must not be set — the unpacker needs to extract.
	info, err := sn.Stat(ctx, activeKey)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Labels[snapshots.LabelSkipApply] != "" {
		t.Errorf("LabelSkipApply should not be set on cache miss, got %q", info.Labels[snapshots.LabelSkipApply])
	}

	// Simulate extraction.
	if err := os.WriteFile(filepath.Join(fsPath, "file"), []byte("content"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := sn.Commit(ctx, name, activeKey, labels); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000001.0.0.0")

	// The extracted file must be accessible from the cache directory.
	if _, err := os.Stat(filepath.Join(cacheDir, "file")); err != nil {
		t.Errorf("extracted file not in cache: %v", err)
	}

	// The committed snapshot's fs/ must be a symlink pointing at the cache entry.
	checkSymlink(t, filepath.Join(getCommittedBasePath(ctx, sn, root, name), "fs"), cacheDir)
}

// testLCCCacheHit verifies the skip-apply path: when cached content already
// exists, Prepare sets LabelSkipApply and creates fs/ as a symlink directly
// without waiting for extraction.
func testLCCCacheHit(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffID = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

	// Seed the cache via a first extraction.
	prepareAndCommitLCC(t, ctx, sn, root, "active-miss", "committed-miss", "", diffID)

	// Second Prepare for the same diffID → cache hit.
	const hitKey = "active-hit"
	labels := snapshots.WithLabels(map[string]string{snapshots.LabelSnapshotDiffID: diffID})
	if _, err := sn.Prepare(ctx, hitKey, "", labels); err != nil {
		t.Fatalf("Prepare (hit): %v", err)
	}

	// LabelSkipApply must be set so the unpacker skips extraction.
	info, err := sn.Stat(ctx, hitKey)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Labels[snapshots.LabelSkipApply] != "true" {
		t.Errorf("LabelSkipApply: got %q, want %q", info.Labels[snapshots.LabelSkipApply], "true")
	}

	// fs/ must already be a symlink — no extraction needed.
	fsPath := filepath.Join(getBasePath(ctx, sn, root, hitKey), "fs")
	fi, err := os.Lstat(fsPath)
	if err != nil {
		t.Fatalf("Lstat fs: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("fs/ should be a symlink on cache hit, got real dir")
	}
}

// testLCCCacheHit_Commit verifies that committing a cache-hit snapshot — whose
// fs/ is already a symlink — preserves the symlink and correctly records the
// cache reference on the committed snapshot.
func testLCCCacheHit_Commit(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffID = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

	prepareAndCommitLCC(t, ctx, sn, root, "active-miss", "committed-miss", "", diffID)
	cacheDir := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000001.0.0.0")

	// Cache-hit Prepare: fs/ is already a symlink.
	labels := snapshots.WithLabels(map[string]string{snapshots.LabelSnapshotDiffID: diffID})
	if _, err := sn.Prepare(ctx, "active-hit", "", labels); err != nil {
		t.Fatalf("Prepare (hit): %v", err)
	}

	// Commit without writing anything — the unpacker skipped apply.
	if err := sn.Commit(ctx, "committed-hit", "active-hit", labels); err != nil {
		t.Fatalf("Commit (hit): %v", err)
	}

	// The committed snapshot's fs/ must still be a symlink to the same cache entry.
	checkSymlink(t, filepath.Join(getCommittedBasePath(ctx, sn, root, "committed-hit"), "fs"), cacheDir)
}

// testLCCConcurrentExtraction verifies the race-loser path in
// commitLCCSnapshotFSDirectory: when a concurrent extractor wins and the cache
// entry already exists, the loser discards its local copy and symlinks to the
// winner's content.
func testLCCConcurrentExtraction(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffID = "sha256:0000000000000000000000000000000000000000000000000000000000000002"
	labels := snapshots.WithLabels(map[string]string{snapshots.LabelSnapshotDiffID: diffID})
	if _, err := sn.Prepare(ctx, "active-loser", "", labels); err != nil {
		t.Fatal(err)
	}

	// Write content into the active snapshot's fs/ (simulating extraction).
	fsPath := filepath.Join(getBasePath(ctx, sn, root, "active-loser"), "fs")
	if err := os.WriteFile(filepath.Join(fsPath, "loser-file"), []byte("loser"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Simulate the winning extractor: pre-populate the cache entry so that
	// os.Rename inside Commit fails with ENOTEMPTY.
	cacheDir := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000002.0.0.0")
	if err := os.Mkdir(cacheDir, 0755); err != nil {
		t.Fatalf("Mkdir cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "winner-file"), []byte("winner"), 0644); err != nil {
		t.Fatalf("write winner: %v", err)
	}

	if err := sn.Commit(ctx, "committed-loser", "active-loser", labels); err != nil {
		t.Fatalf("Commit (race loser): %v", err)
	}

	// The committed snapshot's fs/ must be a symlink to the winner's cache entry.
	checkSymlink(t, filepath.Join(getCommittedBasePath(ctx, sn, root, "committed-loser"), "fs"), cacheDir)

	// The winner's content must be intact; the loser's must have been discarded.
	if _, err := os.Stat(filepath.Join(cacheDir, "winner-file")); err != nil {
		t.Errorf("winner-file should survive in cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "loser-file")); err == nil {
		t.Error("loser-file should have been discarded, but it exists in cache")
	}
}

// testLCCCleanup verifies that orphaned cache entries are removed by Cleanup
// and that live entries survive. The second half of the test removes the last
// remaining reference and confirms the entry is collected on the next Cleanup.
func testLCCCleanup(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffID1 = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	const diffID2 = "sha256:0000000000000000000000000000000000000000000000000000000000000002"

	prepareAndCommitLCC(t, ctx, sn, root, "active-1", "snap-1", "", diffID1)
	prepareAndCommitLCC(t, ctx, sn, root, "active-2", "snap-2", "", diffID2)

	cache1 := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000001.0.0.0")
	cache2 := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000002.0.0.0")

	for _, p := range []string{cache1, cache2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("cache dir missing before remove: %v", err)
		}
	}

	// Remove snap-1; its cache entry becomes orphaned.
	if err := sn.Remove(ctx, "snap-1"); err != nil {
		t.Fatalf("Remove snap-1: %v", err)
	}
	if err := sn.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(cache1); !os.IsNotExist(err) {
		t.Error("orphaned cache1 should have been removed by Cleanup")
	}
	if _, err := os.Stat(cache2); err != nil {
		t.Errorf("live cache2 should not have been removed: %v", err)
	}

	// Remove snap-2; cache2 now has no references and must be collected next.
	if err := sn.Remove(ctx, "snap-2"); err != nil {
		t.Fatalf("Remove snap-2: %v", err)
	}
	if err := sn.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup (second): %v", err)
	}

	if _, err := os.Stat(cache2); !os.IsNotExist(err) {
		t.Error("orphaned cache2 should have been removed by second Cleanup")
	}
}

// testLCCCleanup_SharedEntry verifies that a cache entry shared by multiple
// snapshots is not removed until the last referencing snapshot is gone.
func testLCCCleanup_SharedEntry(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffID = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

	// First snapshot: seeds the cache.
	prepareAndCommitLCC(t, ctx, sn, root, "active-a", "snap-a", "", diffID)

	// Second snapshot for the same diffID: cache hit, fs/ is a symlink.
	labels := snapshots.WithLabels(map[string]string{snapshots.LabelSnapshotDiffID: diffID})
	if _, err := sn.Prepare(ctx, "active-b", "", labels); err != nil {
		t.Fatalf("Prepare snap-b: %v", err)
	}
	if err := sn.Commit(ctx, "snap-b", "active-b", labels); err != nil {
		t.Fatalf("Commit snap-b: %v", err)
	}

	cacheDir := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000001.0.0.0")

	// Remove snap-a: snap-b still references the cache entry, so it must survive.
	if err := sn.Remove(ctx, "snap-a"); err != nil {
		t.Fatalf("Remove snap-a: %v", err)
	}
	if err := sn.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup after snap-a: %v", err)
	}
	if _, err := os.Stat(cacheDir); err != nil {
		t.Errorf("cache dir removed early while snap-b still holds a reference: %v", err)
	}

	// Remove snap-b: now orphaned.
	if err := sn.Remove(ctx, "snap-b"); err != nil {
		t.Fatalf("Remove snap-b: %v", err)
	}
	if err := sn.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup after snap-b: %v", err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Error("cache dir should be removed after the last reference is gone")
	}
}

// testLCCOwnershipIsolation verifies that two snapshots with the same diffID
// but different uid/gid mappings get separate cache entries.
func testLCCOwnershipIsolation(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffID = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

	// Snapshot with root ownership (uid=0, gid=0) — no remap labels.
	prepareAndCommitLCC(t, ctx, sn, root, "active-root", "snap-root", "", diffID)

	cache0 := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000001.0.0.0")
	if _, err := os.Stat(cache0); err != nil {
		t.Fatalf("root-owned cache dir missing: %v", err)
	}

	// Verify that a different uid/gid produces a distinct cache path.
	// uid/gid are normally derived from the host process or idmap labels; here we
	// test the path computation directly, since creating genuinely remapped
	// snapshots requires kernel id-mapping support.
	infoRemapped := snapshots.Info{Labels: map[string]string{
		snapshots.LabelSnapshotDiffID: diffID,
		labelSnapshotUID:              "1000",
		labelSnapshotGID:              "1000",
		labelSnapshotDiffIDSeq:        "0",
	}}
	pathRemapped, err := sn.lcc.contentPath(infoRemapped)
	if err != nil {
		t.Fatalf("lcc.contentPath remapped: %v", err)
	}
	if pathRemapped == cache0 {
		t.Error("remapped and root-owned snapshots share a cache path — isolation broken")
	}
}

// testLCCNoOpWithoutDiffID verifies that LCC is fully transparent for snapshots
// that do not carry LabelSnapshotDiffID (e.g. container-start Prepare calls).
func testLCCNoOpWithoutDiffID(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	// Bypass testSnapshotter.Prepare to omit LabelSnapshotDiffID, simulating a
	// container-start Prepare that carries no diff-ID.
	if _, err := sn.snapshotter.Prepare(ctx, "no-diffid", ""); err != nil {
		t.Fatal(err)
	}

	// No cache entries should be created — the cache dir itself is pre-created
	// by NewSnapshotter but must remain empty for non-LCC snapshots.
	cacheDir := filepath.Join(root, "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("ReadDir cache: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cache dir should be empty for non-LCC snapshot, got %d entries", len(entries))
	}

	// fs/ must be a real directory.
	fsPath := filepath.Join(getBasePath(ctx, sn, root, "no-diffid"), "fs")
	fi, err := os.Lstat(fsPath)
	if err != nil {
		t.Fatalf("Lstat fs: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("fs/ should be a real directory for non-LCC snapshot, got symlink")
	}

	if err := sn.Commit(ctx, "committed-no-lcc", "no-diffid"); err != nil {
		t.Fatal(err)
	}

	// Cache dir must still be empty after commit.
	entries, err = os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("ReadDir cache after commit: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cache dir should be empty after non-LCC commit, got %d entries", len(entries))
	}

	// The committed snapshot's fs/ must also be a real directory, not a symlink.
	committedFS := filepath.Join(getCommittedBasePath(ctx, sn, root, "committed-no-lcc"), "fs")
	fi, err = os.Lstat(committedFS)
	if err != nil {
		t.Fatalf("Lstat committed fs: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("committed non-LCC snapshot fs/ should be a real directory, got symlink")
	}
}

// testLCCApplyLayerSkipsOnCacheHit verifies that pkg/rootfs.ApplyLayerWithOpts
// honours LabelSkipApply when the LCC cache is already populated: the diff.Applier
// must not be called, and the committed snapshot's fs/ must be a symlink.
//
// This exercises the pkg/rootfs path (used by client.(*image).Unpack), which is
// distinct from the core/unpack path exercised by ctr images import.
func testLCCApplyLayerSkipsOnCacheHit(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffID = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

	// Seed the LCC cache under a non-chainID name so the chain snapshot does not
	// exist when ApplyLayerWithOpts is called below; it will enter applyLayers and
	// find the cache already populated.
	prepareAndCommitLCC(t, ctx, sn, root, "active-seed", "snap-seed", "", diffID)

	// Apply must not be called on a cache hit.
	var applyCalls int
	a := &mockApplier{fn: func() {
		applyCalls++
		t.Error("Apply called on cache hit; extraction should be skipped by LabelSkipApply")
	}}

	layer := rootfs.Layer{
		Diff: ocispec.Descriptor{Digest: digest.Digest(diffID)},
		Blob: ocispec.Descriptor{},
	}

	// For a single layer with no parent chain, identity.ChainID equals the diff ID.
	// ApplyLayerWithOpts stats that name first; it is absent, so applyLayers runs.
	applied, err := rootfs.ApplyLayerWithOpts(ctx, layer, nil, sn, a, nil, nil)
	if err != nil {
		t.Fatalf("ApplyLayerWithOpts: %v", err)
	}
	if !applied {
		t.Error("expected applied=true for a new chain snapshot")
	}
	if applyCalls != 0 {
		t.Errorf("Apply called %d time(s) on cache hit; want 0", applyCalls)
	}

	// The committed snapshot's fs/ must be a symlink to the cache entry.
	committedFS := filepath.Join(getCommittedBasePath(ctx, sn, root, diffID), "fs")
	fi, err := os.Lstat(committedFS)
	if err != nil {
		t.Fatalf("Lstat committed fs: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("committed snapshot fs/ should be a symlink on cache hit, got real dir")
	}
}

// testLCCOrphanedStagingPreventsStaleHit verifies the race-prevention property
// of the .orphaned staging mechanism: once lcc.orphanedPaths renames a cache
// directory within the bolt transaction, a subsequent Prepare for the same diff
// ID sees the path absent and takes a cache miss rather than creating a symlink
// to the soon-to-be-deleted staging path.
func testLCCOrphanedStagingPreventsStaleHit(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	// AsynchronousRemove defers disk cleanup to Cleanup(), so Remove() only
	// touches bolt. This keeps the cache directory on disk after Remove so we
	// can manually call lcc.orphanedPaths to simulate the rename-before-delete
	// portion of Cleanup and verify Prepare then takes a cache miss.
	if !sn.asyncRemove {
		t.Skip("test requires AsynchronousRemove")
	}

	const diffID = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

	prepareAndCommitLCC(t, ctx, sn, root, "active-1", "snap-1", "", diffID)

	cacheDir := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000001.0.0.0")
	if _, err := os.Stat(cacheDir); err != nil {
		t.Fatalf("cache dir missing before test: %v", err)
	}

	// With async remove, Remove() only marks the snapshot deleted in bolt;
	// the cache directory remains on disk until Cleanup() is called.
	if err := sn.Remove(ctx, "snap-1"); err != nil {
		t.Fatalf("Remove snap-1: %v", err)
	}

	// Simulate the bolt-transaction portion of Cleanup: rename the orphaned cache
	// dir to a staging path. After this transaction commits, os.Stat(cacheDir)
	// returns ENOENT even though the content still exists at the staging path.
	var staged []string
	if err := sn.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		var err error
		staged, err = sn.lcc.orphanedPaths(ctx)
		return err
	}); err != nil {
		t.Fatalf("lcc.orphanedPaths: %v", err)
	}
	if len(staged) == 0 {
		t.Fatal("expected at least one staged orphaned path")
	}

	// Original path must be absent — a concurrent Prepare after this point must
	// not find it and create a symlink that would break when the staged dir is removed.
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Errorf("cache dir should be absent after staging, got: %v", err)
	}

	// Prepare for the same diffID must see a cache miss.
	labels := snapshots.WithLabels(map[string]string{snapshots.LabelSnapshotDiffID: diffID})
	if _, err := sn.Prepare(ctx, "active-race", "", labels); err != nil {
		t.Fatalf("Prepare after staging: %v", err)
	}
	info, err := sn.Stat(ctx, "active-race")
	if err != nil {
		t.Fatalf("Stat active-race: %v", err)
	}
	if info.Labels[snapshots.LabelSkipApply] == "true" {
		t.Error("Prepare set LabelSkipApply after cache staging: would create a dangling symlink")
	}

	// Cleanup: remove staged paths and the pending active snapshot.
	for _, p := range staged {
		if err := os.RemoveAll(p); err != nil {
			t.Logf("RemoveAll %s: %v", p, err)
		}
	}
	if err := sn.Remove(ctx, "active-race"); err != nil {
		t.Fatalf("Remove active-race: %v", err)
	}
}

// testLCCDuplicateLayer verifies that when the same diffID appears more than
// once in a snapshot's parent chain, each occurrence gets a distinct seq number
// and therefore a distinct cache directory. This is required because OverlayFS
// rejects lower_dir sequences containing duplicate paths.
//
// The test builds the chain: root → X(seq=0) → Y(seq=0) → X(seq=1).
// Both X snapshots must succeed, and their fs/ symlinks must point to distinct
// cache directories.
func testLCCDuplicateLayer(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffIDX = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	const diffIDY = "sha256:0000000000000000000000000000000000000000000000000000000000000002"

	// First occurrence of X: seq=0.
	prepareAndCommitLCC(t, ctx, sn, root, "active-x0", "snap-x0", "", diffIDX)
	// Y in between: seq=0 for Y.
	prepareAndCommitLCC(t, ctx, sn, root, "active-y", "snap-y", "snap-x0", diffIDY)
	// Second occurrence of X, parent chain contains one prior X → seq=1.
	prepareAndCommitLCC(t, ctx, sn, root, "active-x1", "snap-x1", "snap-y", diffIDX)

	cacheX0 := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000001.0.0.0")
	cacheX1 := filepath.Join(root, "cache", "sha256.0000000000000000000000000000000000000000000000000000000000000001.0.0.1")

	if _, err := os.Stat(cacheX0); err != nil {
		t.Errorf("cache dir for X seq=0 missing: %v", err)
	}
	if _, err := os.Stat(cacheX1); err != nil {
		t.Errorf("cache dir for X seq=1 missing: %v", err)
	}
	if cacheX0 == cacheX1 {
		t.Error("both X occurrences share a cache path — OverlayFS would reject duplicate lower_dir entries")
	}

	// snap-x0's fs/ must point to the seq=0 cache dir.
	fsX0 := filepath.Join(getCommittedBasePath(ctx, sn, root, "snap-x0"), "fs")
	checkSymlink(t, fsX0, cacheX0)

	// snap-x1's fs/ must point to the seq=1 cache dir.
	fsX1 := filepath.Join(getCommittedBasePath(ctx, sn, root, "snap-x1"), "fs")
	checkSymlink(t, fsX1, cacheX1)
}

// testLCCDuplicateLayerCacheHit verifies that a snapshot whose parent chain
// contains exactly one prior occurrence of the same diffID (seq=1) gets a cache
// hit when the seq=1 directory already exists — i.e. seq is included in the
// cache key for skip-apply decisions.
func testLCCDuplicateLayerCacheHit(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffIDX = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	const diffIDY = "sha256:0000000000000000000000000000000000000000000000000000000000000002"

	// Build chain root → X(seq=0) → Y(seq=0) → X(seq=1), seeding both cache dirs.
	prepareAndCommitLCC(t, ctx, sn, root, "active-x0", "snap-x0", "", diffIDX)
	prepareAndCommitLCC(t, ctx, sn, root, "active-y", "snap-y", "snap-x0", diffIDY)
	prepareAndCommitLCC(t, ctx, sn, root, "active-x1", "snap-x1", "snap-y", diffIDX)

	// Now prepare another snapshot whose parent chain also contains exactly one
	// prior X (snap-x0 via snap-y) → seq=1. The seq=1 cache dir already exists
	// from snap-x1, so this must be a cache hit.
	labels := snapshots.WithLabels(map[string]string{snapshots.LabelSnapshotDiffID: diffIDX})
	if _, err := sn.Prepare(ctx, "active-x1-hit", "snap-y", labels); err != nil {
		t.Fatalf("Prepare (seq=1 hit): %v", err)
	}

	info, err := sn.Stat(ctx, "active-x1-hit")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Labels[snapshots.LabelSkipApply] != "true" {
		t.Errorf("LabelSkipApply: got %q, want %q — seq=1 cache dir should trigger skip-apply", info.Labels[snapshots.LabelSkipApply], "true")
	}

	fsPath := filepath.Join(getBasePath(ctx, sn, root, "active-x1-hit"), "fs")
	fi, err := os.Lstat(fsPath)
	if err != nil {
		t.Fatalf("Lstat fs: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("fs/ should be a symlink on seq=1 cache hit, got real dir")
	}
}

// testLCCCountDiffIDInChain directly tests lccState.diffIDSeqInChain by building
// committed snapshots and verifying the count at each point in the chain.
func testLCCCountDiffIDInChain(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sn := o.(*testSnapshotter)

	const diffIDX = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	const diffIDY = "sha256:0000000000000000000000000000000000000000000000000000000000000002"

	// Build chain: root → X → Y → X.
	prepareAndCommitLCC(t, ctx, sn, root, "a-x0", "snap-x0", "", diffIDX)
	prepareAndCommitLCC(t, ctx, sn, root, "a-y", "snap-y", "snap-x0", diffIDY)
	prepareAndCommitLCC(t, ctx, sn, root, "a-x1", "snap-x1", "snap-y", diffIDX)

	for _, tc := range []struct {
		name   string
		diffID string
		parent string
		want   int
	}{
		// No parent: count is always 0.
		{"X at root", diffIDX, "", 0},
		{"Y at root", diffIDY, "", 0},
		// After snap-x0 (one X): X count is 1, Y count is 0.
		{"X after snap-x0", diffIDX, "snap-x0", 1},
		{"Y after snap-x0", diffIDY, "snap-x0", 0},
		// After snap-y (one X, one Y): X count is still 1, Y count is 1.
		{"X after snap-y", diffIDX, "snap-y", 1},
		{"Y after snap-y", diffIDY, "snap-y", 1},
		// After snap-x1 (two X, one Y): X count is 2, Y count is 1.
		{"X after snap-x1", diffIDX, "snap-x1", 2},
		{"Y after snap-x1", diffIDY, "snap-x1", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sn.lcc.diffIDSeqInChain(tc.parent, tc.diffID)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// checkSymlink asserts that path is a symlink whose resolved target equals want.
func checkSymlink(t *testing.T, path, want string) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %q: %v", path, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%q should be a symlink, got regular file/dir", path)
	}
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("Readlink %q: %v", path, err)
	}
	abs := filepath.Join(filepath.Dir(path), target)
	if abs != want {
		t.Errorf("symlink %q → %q, want %q", path, abs, want)
	}
}
