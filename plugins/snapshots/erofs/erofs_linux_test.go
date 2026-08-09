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

package erofs

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/mount"
	mountmanager "github.com/containerd/containerd/v2/core/mount/manager"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"
	"github.com/containerd/containerd/v2/internal/dmverity"
	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/containerd/containerd/v2/internal/fsverity"
	"github.com/containerd/containerd/v2/pkg/archive/tartest"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/plugins/content/local"
	erofsdiffer "github.com/containerd/containerd/v2/plugins/diff/erofs"
	erofsmount "github.com/containerd/containerd/v2/plugins/mount/erofs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	testFileContent       = "Hello, this is content for testing the EROFS Snapshotter!"
	testNestedFileContent = "Nested file content"
	testDmverityMetadata  = `{
  "roothash": "fedcba098765432109876543210987654321098765432109876543210987",
  "hashoffset": 4096
}`
)

func newSnapshotter(t *testing.T, opts ...Opt) func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	_, err := exec.LookPath("mkfs.erofs")
	if err != nil {
		t.Skipf("could not find mkfs.erofs: %v", err)
	}

	if !FindErofs() {
		t.Skip("check for erofs kernel support failed, skipping test")
	}
	return func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		snapshotter, err := NewSnapshotter(root, opts...)
		if err != nil {
			return nil, nil, err
		}

		return snapshotter, func() error { return snapshotter.Close() }, nil
	}
}

func testMount(t *testing.T, scratchFile string) error {
	root := t.TempDir()
	m := []mount.Mount{
		{
			Type:    "ext4",
			Source:  scratchFile,
			Options: []string{"loop", "direct-io", "sync"},
		},
	}

	if err := mount.All(m, root); err != nil {
		return fmt.Errorf("failed to mount device %s: %w", scratchFile, err)
	}

	if err := os.Remove(filepath.Join(root, "lost+found")); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(root, "work"), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(root, "upper"), 0755); err != nil {
		return err
	}
	return mount.UnmountAll(root, 0)
}

func TestErofs(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "erofs", newSnapshotter(t))
}

func TestErofsWithQuota(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "erofs", newSnapshotter(t, WithDefaultSize(16*1024*1024)))
}

func TestGetCleanupDirectoriesSkipsSnapshotTempDirs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	snapshotDir := filepath.Join(root, "snapshots")
	require.NoError(t, os.Mkdir(snapshotDir, 0700))

	ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ms.Close()) })
	s := &snapshotter{root: root, ms: ms}

	_, err = os.MkdirTemp(snapshotDir, snapshotTempDirPrefix)
	require.NoError(t, err)
	orphanDir := filepath.Join(snapshotDir, "orphan")
	require.NoError(t, os.Mkdir(orphanDir, 0700))
	require.NoError(t, ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, err := storage.CreateSnapshot(ctx, snapshots.KindActive, "existing", "")
		return err
	}))

	var cleanup []string
	require.NoError(t, ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		cleanup, err = s.getCleanupDirectories(ctx)
		return err
	}))
	assert.Equal(t, []string{orphanDir}, cleanup)
}

// TestWritableSize exercises the LabelSnapshotMaxSize override that the
// block-mode mkfs path passes to X-containerd.mkfs.size. Covers the
// happy path (label overrides default), fallback cases (missing, empty,
// malformed, non-positive), and that a valid label wins over a non-zero
// configured default.
func TestWritableSize(t *testing.T) {
	const defaultSize = int64(16 * 1024 * 1024)
	s := &snapshotter{defaultWritable: defaultSize}

	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   int64
	}{
		{"unset", nil, defaultSize},
		{"empty-map", map[string]string{}, defaultSize},
		{"valid-overrides-default", map[string]string{snapshots.LabelSnapshotMaxSize: "268435456"}, 268435456},
		{"empty-value-falls-back", map[string]string{snapshots.LabelSnapshotMaxSize: ""}, defaultSize},
		{"non-numeric-falls-back", map[string]string{snapshots.LabelSnapshotMaxSize: "100MB"}, defaultSize},
		{"zero-falls-back", map[string]string{snapshots.LabelSnapshotMaxSize: "0"}, defaultSize},
		{"negative-falls-back", map[string]string{snapshots.LabelSnapshotMaxSize: "-1"}, defaultSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.writableSize(snapshots.Info{Labels: tc.labels})
			assert.Equal(t, tc.want, got)
		})
	}

	// Also verify behaviour when the snapshotter has no configured default:
	// an unset/invalid label yields 0 (caller treats as "no size"), a valid
	// label is respected.
	t.Run("no-default-unset", func(t *testing.T) {
		z := &snapshotter{defaultWritable: 0}
		assert.Equal(t, int64(0), z.writableSize(snapshots.Info{}))
	})
	t.Run("no-default-with-label", func(t *testing.T) {
		z := &snapshotter{defaultWritable: 0}
		got := z.writableSize(snapshots.Info{Labels: map[string]string{
			snapshots.LabelSnapshotMaxSize: "1048576",
		}})
		assert.Equal(t, int64(1048576), got)
	})
}

func TestErofsFsverity(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := context.Background()

	root := t.TempDir()

	// Skip if fsverity is not supported
	supported, err := fsverity.IsSupported(root)
	if !supported || err != nil {
		t.Skip("fsverity not supported, skipping test")
	}

	// Create snapshotter with fsverity enabled
	s, err := NewSnapshotter(root, WithFsverity())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create a test snapshot
	key := "test-snapshot"
	mounts, err := s.Prepare(ctx, key, "")
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, key)
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := mount.All(mounts, target); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, target)

	// Write test data
	if err := os.WriteFile(filepath.Join(target, "foo"), []byte("test data"), 0777); err != nil {
		t.Fatal(err)
	}

	// Commit the snapshot
	commitKey := "test-commit"
	if err := s.Commit(ctx, commitKey, key); err != nil {
		t.Fatal(err)
	}

	snap := s.(*snapshotter)

	// Get the internal ID from the snapshotter
	var id string
	if err := snap.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, _, _, err = storage.GetInfo(ctx, commitKey)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Verify fsverity is enabled on the EROFS layer

	layerPath := snap.layerBlobPath(id)

	enabled, err := fsverity.IsEnabled(layerPath)
	if err != nil {
		t.Fatalf("Failed to check fsverity status: %v", err)
	}
	if !enabled {
		t.Fatal("Expected fsverity to be enabled on committed layer")
	}

	// Try to modify the layer file directly (should fail)
	if err := os.WriteFile(layerPath, []byte("tampered data"), 0666); err == nil {
		t.Fatal("Expected direct write to fsverity-enabled layer to fail")
	}
}

func TestErofsDifferWithTarIndexMode(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := context.Background()

	if !FindErofs() {
		t.Skip("check for erofs kernel support failed, skipping test")
	}

	// Check if mkfs.erofs supports tar index mode
	supported, err := erofsutils.SupportGenerateFromTar()
	if err != nil || !supported {
		t.Skip("mkfs.erofs does not support tar mode, skipping tar index test")
	}

	tempDir := t.TempDir()

	// Create content store for the differ
	contentStore, err := local.NewStore(filepath.Join(tempDir, "content"))
	if err != nil {
		t.Fatal(err)
	}

	// Create EROFS differ with tar index mode enabled
	differ := erofsdiffer.NewErofsDiffer(contentStore, erofsdiffer.WithTarIndexMode())

	// Create EROFS snapshotter
	snapshotRoot := filepath.Join(tempDir, "snapshots")
	s, err := NewSnapshotter(snapshotRoot)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	// Create test tar content
	tarReader := createTestTarContent()
	defer tarReader.Close()

	// Read the tar content into a buffer for digest calculation and writing
	tarContent, err := io.ReadAll(tarReader)
	if err != nil {
		t.Fatal(err)
	}

	// Write tar content to content store
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(tarContent),
		Size:      int64(len(tarContent)),
	}

	writer, err := contentStore.Writer(ctx,
		content.WithRef("test-layer"),
		content.WithDescriptor(desc))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := writer.Write(tarContent); err != nil {
		writer.Close()
		t.Fatal(err)
	}

	if err := writer.Commit(ctx, desc.Size, desc.Digest); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()

	// Prepare a snapshot using the snapshotter
	snapshotKey := "test-snapshot"
	mounts, err := s.Prepare(ctx, snapshotKey, "")
	if err != nil {
		t.Fatal(err)
	}

	// Apply the tar content using the EROFS differ with tar index mode
	appliedDesc, err := differ.Apply(ctx, desc, mounts)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Applied layer using EROFS differ with tar index mode:")
	t.Logf("  Original: %s (%d bytes)", desc.Digest, desc.Size)
	t.Logf("  Applied:  %s (%d bytes)", appliedDesc.Digest, appliedDesc.Size)
	t.Logf("  MediaType: %s", appliedDesc.MediaType)

	// Commit the snapshot to finalize the EROFS layer creation
	commitKey := "test-commit"
	if err := s.Commit(ctx, commitKey, snapshotKey); err != nil {
		t.Fatal(err)
	}

	// Get the internal snapshot ID to check the EROFS layer file
	snap := s.(*snapshotter)
	var id string
	if err := snap.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, _, _, err = storage.GetInfo(ctx, commitKey)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Verify the EROFS layer file was created
	layerPath := snap.layerBlobPath(id)
	if _, err := os.Stat(layerPath); err != nil {
		t.Fatalf("EROFS layer file should exist: %v", err)
	}

	// Verify the layer file is not empty
	stat, err := os.Stat(layerPath)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() == 0 {
		t.Fatal("EROFS layer file should not be empty")
	}

	t.Logf("EROFS layer file created with tar index mode: %s (%d bytes)", layerPath, stat.Size())

	// Create a view to verify the content
	viewKey := "test-view"
	viewMounts, err := s.View(ctx, viewKey, commitKey)
	if err != nil {
		t.Fatal(err)
	}

	viewTarget := filepath.Join(tempDir, viewKey)
	if err := os.MkdirAll(viewTarget, 0755); err != nil {
		t.Fatal(err)
	}
	if err := mount.All(viewMounts, viewTarget); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, viewTarget)

	// Verify we can read the original test data
	testData, err := os.ReadFile(filepath.Join(viewTarget, "test-file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	expected := testFileContent
	if string(testData) != expected {
		t.Fatalf("Expected %q, got %q", expected, string(testData))
	}

	// Verify nested file
	nestedData, err := os.ReadFile(filepath.Join(viewTarget, "testdir", "nested.txt"))
	if err != nil {
		t.Fatal(err)
	}
	expectedNested := testNestedFileContent
	if string(nestedData) != expectedNested {
		t.Fatalf("Expected %q, got %q", expectedNested, string(nestedData))
	}

	t.Logf("Successfully verified EROFS Snapshotter using the differ with tar index mode")
}

// Helper function to create test tar content using tartest
func createTestTarContent() io.ReadCloser {
	// Create a tar context with current time for consistency
	tc := tartest.TarContext{}.WithModTime(time.Now())

	// Create the tar with our test files and directories
	tarWriter := tartest.TarAll(
		tc.File("test-file.txt", []byte(testFileContent), 0644),
		tc.Dir("testdir", 0755),
		tc.File("testdir/nested.txt", []byte(testNestedFileContent), 0644),
	)

	// Return the tar as a ReadCloser
	return tartest.TarFromWriterTo(tarWriter)
}

// Helper to create a dm-verity metadata file for testing
func createDmverityMetadata(t *testing.T, layerBlob string) {
	t.Helper()
	metadataPath := layerBlob + ".dmverity"
	err := os.WriteFile(metadataPath, []byte(testDmverityMetadata), 0644)
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(metadataPath) })
}

// Helper to create a test layer blob file
func createTestLayerBlob(t *testing.T, dir string) string {
	t.Helper()
	layerBlob := filepath.Join(dir, "layer.erofs")
	err := os.WriteFile(layerBlob, []byte{}, 0644)
	require.NoError(t, err)
	return layerBlob
}

// TestCreateErofsMount tests mount creation without dm-verity
func TestCreateErofsMount(t *testing.T) {
	tmpDir := t.TempDir()
	layerBlob := createTestLayerBlob(t, tmpDir)

	s := &snapshotter{
		root:         tmpDir,
		dmverityMode: "off",
	}

	t.Run("creates regular erofs mount", func(t *testing.T) {
		m, err := s.createErofsMount(layerBlob)
		require.NoError(t, err)

		assert.Equal(t, "erofs", m.Type)
		assert.Equal(t, layerBlob, m.Source)
		// No X-containerd.dmverity option needed since no .dmverity metadata exists
		assert.Equal(t, []string{"ro", "loop"}, m.Options)
	})

	t.Run("always returns erofs mount type", func(t *testing.T) {
		s.dmverityMode = "on"
		createDmverityMetadata(t, layerBlob)

		m, err := s.createErofsMount(layerBlob)
		require.NoError(t, err)
		// Mount type is always "erofs" - dm-verity detection happens in mount handler
		assert.Equal(t, "erofs", m.Type)
		assert.Equal(t, layerBlob, m.Source)
		assert.Contains(t, m.Options, "ro")
		assert.Contains(t, m.Options, "loop")
	})

	t.Run("mode off skips dm-verity even when metadata exists", func(t *testing.T) {
		metadataFile := layerBlob + ".dmverity"
		metadataContent := `{
  "roothash": "fedcba098765432109876543210987654321098765432109876543210987",
  "hashoffset": 4096
}`
		require.NoError(t, os.WriteFile(metadataFile, []byte(metadataContent), 0644))

		s.dmverityMode = "off"

		m, err := s.createErofsMount(layerBlob)
		require.NoError(t, err)

		assert.Equal(t, "erofs", m.Type)
		assert.Equal(t, layerBlob, m.Source)
		// mode "off" skips dm-verity entirely - no dm-verity option in mount options
		for _, opt := range m.Options {
			assert.NotContains(t, opt, "X-containerd.dmverity=")
		}
	})
}

// TestDmverityEndToEnd tests the full workflow: differ creates dm-verity layer,
// snapshotter mounts it via mount manager, and cleanup on removal
func TestDmverityEndToEnd(t *testing.T) {
	testutil.RequiresRoot(t)

	supported, err := dmverity.IsSupported()
	if err != nil || !supported {
		t.Skip("dm-verity is not supported on this system")
	}

	t.Run("with regular mode", func(t *testing.T) {
		testDmverityEndToEndWithMode(t, false)
	})

	tarSupported, err := erofsutils.SupportGenerateFromTar()
	if err == nil && tarSupported {
		t.Run("with tar index mode", func(t *testing.T) {
			testDmverityEndToEndWithMode(t, true)
		})
	} else {
		t.Logf("Skipping tar index mode test: mkfs.erofs does not support tar mode")
	}
}

func testDmverityEndToEndWithMode(t *testing.T, useTarIndex bool) {
	ctx := context.Background()
	ctx = namespaces.WithNamespace(ctx, "test")
	tempDir := t.TempDir()

	metadb := filepath.Join(tempDir, "mounts.db")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	defer db.Close()

	mountTargetDir := filepath.Join(tempDir, "mount-manager")
	mgr, err := mountmanager.NewManager(db, mountTargetDir,
		mountmanager.WithMountHandler("erofs", erofsmount.NewErofsMountHandler()))
	require.NoError(t, err)

	contentStore, err := local.NewStore(filepath.Join(tempDir, "content"))
	require.NoError(t, err)

	var differOpts []erofsdiffer.DifferOpt
	differOpts = append(differOpts, erofsdiffer.WithDmverity())
	if useTarIndex {
		differOpts = append(differOpts, erofsdiffer.WithTarIndexMode())
	}
	differ := erofsdiffer.NewErofsDiffer(contentStore, differOpts...)

	snapshotRoot := filepath.Join(tempDir, "snapshots")
	sn, err := NewSnapshotter(snapshotRoot, WithDmverityMode("on"))
	require.NoError(t, err)
	defer sn.Close()

	s := sn.(*snapshotter)

	tarReader := createTestTarContent()
	defer tarReader.Close()

	tarContent, err := io.ReadAll(tarReader)
	require.NoError(t, err)

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(tarContent),
		Size:      int64(len(tarContent)),
	}

	writer, err := contentStore.Writer(ctx,
		content.WithRef("test-layer"),
		content.WithDescriptor(desc))
	require.NoError(t, err)

	_, err = writer.Write(tarContent)
	require.NoError(t, err)

	err = writer.Commit(ctx, desc.Size, desc.Digest)
	require.NoError(t, err)
	writer.Close()

	// Prepare snapshot
	snapshotKey := "test-snapshot"
	mounts, err := sn.Prepare(ctx, snapshotKey, "")
	require.NoError(t, err)

	_, err = differ.Apply(ctx, desc, mounts)
	require.NoError(t, err)

	commitKey := "test-commit"
	err = sn.Commit(ctx, commitKey, snapshotKey)
	require.NoError(t, err)

	var snapshotID string
	err = s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		var err error
		snapshotID, _, _, err = storage.GetInfo(ctx, commitKey)
		return err
	})
	require.NoError(t, err)

	// Differ should create .dmverity metadata alongside layer
	layerPath := s.layerBlobPath(snapshotID)
	metadataPath := layerPath + ".dmverity"

	metadataData, err := os.ReadFile(metadataPath)
	require.NoError(t, err, ".dmverity file should exist")
	require.NotEmpty(t, metadataData, "metadata should not be empty")

	viewKey := "test-view"
	viewMounts, err := sn.View(ctx, viewKey, commitKey)
	require.NoError(t, err)

	// Mount handler (not snapshotter) activates dm-verity
	require.Len(t, viewMounts, 1)
	assert.Equal(t, "erofs", viewMounts[0].Type)
	assert.Contains(t, viewMounts[0].Options, "ro")
	assert.Contains(t, viewMounts[0].Options, "loop")

	viewTarget := filepath.Join(tempDir, "view-mount")
	require.NoError(t, os.MkdirAll(viewTarget, 0755))

	mountID := "test-view-mount"
	activateInfo, err := mgr.Activate(ctx, mountID, viewMounts)
	require.NoError(t, err)

	// EROFS handler mounts directly, check Active mounts for the actual mount point
	require.Len(t, activateInfo.Active, 1, "should have one active mount from EROFS handler")
	actualMountPoint := activateInfo.Active[0].MountPoint
	require.NotEmpty(t, actualMountPoint, "mount point should be set by EROFS handler")

	testData, err := os.ReadFile(filepath.Join(actualMountPoint, "test-file.txt"))
	require.NoError(t, err, "should be able to read test file from dm-verity mount")
	assert.Equal(t, testFileContent, string(testData))

	nestedData, err := os.ReadFile(filepath.Join(actualMountPoint, "testdir", "nested.txt"))
	require.NoError(t, err, "should be able to read nested file from dm-verity mount")
	assert.Equal(t, testNestedFileContent, string(nestedData))

	err = mgr.Deactivate(ctx, mountID)
	require.NoError(t, err)

	err = sn.Remove(ctx, viewKey)
	require.NoError(t, err)

	err = sn.Remove(ctx, commitKey)
	require.NoError(t, err)

	err = s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, err := storage.GetSnapshot(ctx, commitKey)
		return err
	})
	assert.Error(t, err, "snapshot should be removed from metadata")
}

// TestDmverityModeValidation tests dm-verity mode validation during snapshotter creation
func TestDmverityModeValidation(t *testing.T) {
	testutil.RequiresRoot(t)
	tmpDir := t.TempDir()

	t.Run("rejects invalid dmverity mode", func(t *testing.T) {
		_, err := NewSnapshotter(tmpDir, WithDmverityMode("invalid-mode"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dmverity_mode")
		assert.Contains(t, err.Error(), `must be "auto", "on", or "off"`)
	})

	t.Run("accepts valid auto mode", func(t *testing.T) {
		root := filepath.Join(tmpDir, "auto")
		s, err := NewSnapshotter(root, WithDmverityMode("auto"))
		require.NoError(t, err)
		assert.NotNil(t, s)
		s.Close()
	})

	t.Run("accepts valid on mode when dm-verity is supported", func(t *testing.T) {
		supported, err := dmverity.IsSupported()
		if err != nil || !supported {
			t.Skip("dm-verity not supported, skipping")
		}

		root := filepath.Join(tmpDir, "on")
		s, err := NewSnapshotter(root, WithDmverityMode("on"))
		require.NoError(t, err)
		assert.NotNil(t, s)
		s.Close()
	})

	t.Run("accepts valid off mode", func(t *testing.T) {
		root := filepath.Join(tmpDir, "off")
		s, err := NewSnapshotter(root, WithDmverityMode("off"))
		require.NoError(t, err)
		assert.NotNil(t, s)
		s.Close()
	})

	t.Run("defaults to auto mode when not specified", func(t *testing.T) {
		root := filepath.Join(tmpDir, "default")
		s, err := NewSnapshotter(root)
		require.NoError(t, err)
		snap := s.(*snapshotter)
		assert.Equal(t, "auto", snap.dmverityMode)
		s.Close()
	})
}

// TestApplyDmverityPolicy tests the dm-verity policy application logic
func TestApplyDmverityPolicy(t *testing.T) {
	testutil.RequiresRoot(t)
	tmpDir := t.TempDir()
	layerBlob := createTestLayerBlob(t, tmpDir)

	t.Run("mode on requires metadata to exist", func(t *testing.T) {
		s := &snapshotter{
			dmverityMode: "on",
		}

		_, err := s.applyDmverityPolicy(layerBlob)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dm-verity mode is 'on' but .dmverity metadata not found")
		assert.Contains(t, err.Error(), "layer was created before dm-verity was enabled")
	})

	t.Run("mode auto returns empty string when no metadata", func(t *testing.T) {
		s := &snapshotter{
			dmverityMode: "auto",
		}

		opt, err := s.applyDmverityPolicy(layerBlob)
		require.NoError(t, err)
		assert.Empty(t, opt)
	})

	t.Run("mode off returns empty string when metadata exists", func(t *testing.T) {
		createDmverityMetadata(t, layerBlob)

		s := &snapshotter{
			dmverityMode: "off",
		}

		opt, err := s.applyDmverityPolicy(layerBlob)
		require.NoError(t, err)
		assert.Empty(t, opt)
	})

	t.Run("mode on returns metadata path when metadata exists", func(t *testing.T) {
		createDmverityMetadata(t, layerBlob)

		s := &snapshotter{
			dmverityMode: "on",
		}

		opt, err := s.applyDmverityPolicy(layerBlob)
		require.NoError(t, err)
		expectedPath := layerBlob + ".dmverity"
		assert.Equal(t, "X-containerd.dmverity="+expectedPath, opt)
	})

	t.Run("mode auto returns metadata path when metadata exists", func(t *testing.T) {
		createDmverityMetadata(t, layerBlob)

		s := &snapshotter{
			dmverityMode: "auto",
		}

		opt, err := s.applyDmverityPolicy(layerBlob)
		require.NoError(t, err)
		expectedPath := layerBlob + ".dmverity"
		assert.Equal(t, "X-containerd.dmverity="+expectedPath, opt)
	})
}

func TestMountFsMeta(t *testing.T) {
	root := t.TempDir()
	s := &snapshotter{root: root}

	parents := []string{"p0", "p1", "p2"}
	for _, id := range parents {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "snapshots", id), 0755))
	}

	writeMeta := func(t *testing.T, id string, contents []byte) {
		t.Helper()
		require.NoError(t, os.WriteFile(s.fsMetaPath(id), contents, 0644))
	}
	removeMeta := func(t *testing.T, id string) {
		t.Helper()
		err := os.Remove(s.fsMetaPath(id))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	snap := storage.Snapshot{ParentIDs: parents}

	t.Run("missing fsmeta returns false", func(t *testing.T) {
		for _, id := range parents {
			removeMeta(t, id)
		}
		_, ok := s.mountFsMeta(snap, 0)
		assert.False(t, ok)
	})

	t.Run("empty fsmeta returns false", func(t *testing.T) {
		writeMeta(t, "p0", nil)
		t.Cleanup(func() { removeMeta(t, "p0") })

		_, ok := s.mountFsMeta(snap, 0)
		assert.False(t, ok)
	})

	t.Run("non-empty fsmeta on top parent returns mount with all device options", func(t *testing.T) {
		writeMeta(t, "p0", []byte("merged"))
		t.Cleanup(func() { removeMeta(t, "p0") })

		m, ok := s.mountFsMeta(snap, 0)
		require.True(t, ok)
		assert.Equal(t, "erofs", m.Type)
		assert.Equal(t, s.fsMetaPath("p0"), m.Source)
		// Devices appended in reverse parent order from len-1 down to id.
		assert.Equal(t, []string{
			"ro", "loop",
			"device=" + s.layerBlobPath("p2"),
			"device=" + s.layerBlobPath("p1"),
			"device=" + s.layerBlobPath("p0"),
		}, m.Options)
	})

	t.Run("non-empty fsmeta on intermediate parent only references parents at or below id", func(t *testing.T) {
		writeMeta(t, "p1", []byte("merged"))
		t.Cleanup(func() { removeMeta(t, "p1") })

		m, ok := s.mountFsMeta(snap, 1)
		require.True(t, ok)
		assert.Equal(t, s.fsMetaPath("p1"), m.Source)
		assert.Equal(t, []string{
			"ro", "loop",
			"device=" + s.layerBlobPath("p2"),
			"device=" + s.layerBlobPath("p1"),
		}, m.Options)
	})
}

// TestMountsWithMergedFsMeta covers s.mounts()'s assembly of the overlay
// lowerdir when a merged fsmeta is present on a parent below the top of the
// chain, i.e. one or more plain (non-merged) layers are stacked on top of a
// merged fsmeta lower.
func TestMountsWithMergedFsMeta(t *testing.T) {
	root := t.TempDir()
	s := &snapshotter{root: root}

	// Chain (top to bottom): p0, p1, p2, p3. A merged fsmeta is only
	// present for p2, flattening the sub-chain [p2, p3]. p0 and p1 are
	// plain layers stacked on top of that merged base.
	parents := []string{"p0", "p1", "p2", "p3"}
	for _, id := range parents {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "snapshots", id), 0755))
		require.NoError(t, os.WriteFile(s.layerBlobPath(id), []byte("layer"), 0644))
	}
	require.NoError(t, os.WriteFile(s.fsMetaPath("p2"), []byte("merged"), 0644))

	snap := storage.Snapshot{Kind: snapshots.KindView, ParentIDs: parents}
	info := snapshots.Info{}

	mounts, err := s.mounts(snap, info)
	require.NoError(t, err)

	// Expect: [erofs(p0), erofs(p1), erofs(fsmeta p2, device=p3,p2), overlay]
	require.Len(t, mounts, 4)

	assert.Equal(t, "erofs", mounts[0].Type)
	assert.Equal(t, s.layerBlobPath("p0"), mounts[0].Source)

	assert.Equal(t, "erofs", mounts[1].Type)
	assert.Equal(t, s.layerBlobPath("p1"), mounts[1].Source)

	assert.Equal(t, "erofs", mounts[2].Type)
	assert.Equal(t, s.fsMetaPath("p2"), mounts[2].Source)
	assert.Equal(t, []string{
		"ro", "loop",
		"device=" + s.layerBlobPath("p3"),
		"device=" + s.layerBlobPath("p2"),
	}, mounts[2].Options)

	// The overlay must span all three lowers (indices 0-2): the two plain
	// top layers plus the merged fsmeta.
	overlay := mounts[3]
	assert.Equal(t, "format/mkdir/overlay", overlay.Type)
	assert.Contains(t, overlay.Options, "lowerdir={{ overlay 0 2 }}")
}

// TestMountsWithMergedFsMetaOnTopParent covers the case where the merged
// fsmeta is valid for the topmost parent, so it is the only lower. Since
// overlayfs rejects a lowerdir with no upperdir, this collapses to a plain
// bind mount instead.
func TestMountsWithMergedFsMetaOnTopParent(t *testing.T) {
	root := t.TempDir()
	s := &snapshotter{root: root}

	parents := []string{"p0", "p1"}
	for _, id := range parents {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "snapshots", id), 0755))
		require.NoError(t, os.WriteFile(s.layerBlobPath(id), []byte("layer"), 0644))
	}
	require.NoError(t, os.WriteFile(s.fsMetaPath("p0"), []byte("merged"), 0644))

	snap := storage.Snapshot{Kind: snapshots.KindView, ParentIDs: parents}
	info := snapshots.Info{}

	mounts, err := s.mounts(snap, info)
	require.NoError(t, err)

	require.Len(t, mounts, 2)
	assert.Equal(t, "erofs", mounts[0].Type)
	assert.Equal(t, s.fsMetaPath("p0"), mounts[0].Source)
	assert.Equal(t, []string{
		"ro", "loop",
		"device=" + s.layerBlobPath("p1"),
		"device=" + s.layerBlobPath("p0"),
	}, mounts[0].Options)

	assert.Equal(t, "format/bind", mounts[1].Type)
	assert.Equal(t, "{{ mount 0 }}", mounts[1].Source)
}

// --- layer content cache tests ---

const (
	cacheTestDiffID  = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	cacheTestChainID = "sha256:0000000000000000000000000000000000000000000000000000000000000002"
)

// requireErofs skips a test unless the erofs kernel filesystem is available,
// which NewSnapshotter requires. The layer content cache tests don't mount, so
// they need neither root nor mkfs.erofs.
func requireErofs(t *testing.T) {
	t.Helper()
	if !FindErofs() {
		t.Skip("check for erofs kernel support failed, skipping test")
	}
}

// writeCacheBlob writes a fake erofs blob into cacheDir keyed by diffID and
// returns its absolute path. The bytes need not be a valid erofs image: the
// snapshotter only symlinks the blob, so these tests exercise the
// Prepare/Commit/Remove logic rather than mounting.
func writeCacheBlob(t *testing.T, cacheDir string, diffID digest.Digest, data []byte) string {
	t.Helper()
	blob := erofsutils.CacheBlobPath(cacheDir, diffID)
	require.NoError(t, os.MkdirAll(filepath.Dir(blob), 0755))
	require.NoError(t, os.WriteFile(blob, data, 0644))
	return blob
}

// extractionOpt builds the snapshot options the unpacker attaches to an
// image-layer extraction Prepare (the snapshot.ref target and the diffID).
func extractionOpt(target string, diffID digest.Digest) snapshots.Opt {
	return snapshots.WithLabels(map[string]string{
		snapshots.LabelSnapshotRef:    target,
		snapshots.LabelSnapshotDiffID: diffID.String(),
	})
}

// snapshotID returns the backend snapshot ID for key.
func snapshotID(t *testing.T, ctx context.Context, s *snapshotter, key string) string {
	t.Helper()
	var id string
	require.NoError(t, s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		var err error
		id, _, _, err = storage.GetInfo(ctx, key)
		return err
	}))
	return id
}

// newCacheSnapshotter creates an erofs snapshotter rooted in a temp dir with the
// given options, skipping the test if erofs is unavailable and registering the
// snapshotter's cleanup.
func newCacheSnapshotter(t *testing.T, opts ...Opt) *snapshotter {
	t.Helper()
	requireErofs(t)
	sn, err := NewSnapshotter(t.TempDir(), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { sn.Close() })
	return sn.(*snapshotter)
}

// stageCacheHit runs an extraction Prepare for target/diffID and asserts the
// cache staged the blob into the active snapshot: a read-only mount, with no
// error (so the unpacker skips fetch+apply but still commits). It returns the
// extraction key so the caller can Commit it as the target chainID.
func stageCacheHit(t *testing.T, ctx context.Context, s *snapshotter, target string, diffID digest.Digest) string {
	t.Helper()
	key := "extract-1 " + target
	mounts, err := s.Prepare(ctx, key, "", extractionOpt(target, diffID))
	require.NoError(t, err, "cache hit must stage without an error")
	require.NotEmpty(t, mounts, "a staged cache hit returns mounts")
	for _, m := range mounts {
		assert.True(t, m.ReadOnly(), "a staged cache hit returns read-only mounts")
	}
	return key
}

// TestCacheHit covers the happy path: an extraction Prepare whose diffID blob is
// in the cache stages the blob into the active snapshot (symlinked) and returns
// read-only mounts without committing; a subsequent Commit finalizes the target
// chainID without re-converting.
func TestCacheHit(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	blob := writeCacheBlob(t, cacheDir, diffID, []byte("fake erofs blob"))

	s := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	target := cacheTestChainID
	key := stageCacheHit(t, ctx, s, target, diffID)

	// The blob is staged into the active snapshot but the target chainID is not
	// committed yet.
	_, err := s.Stat(ctx, target)
	assert.Error(t, err, "target chainID must not be committed before Commit")

	// layer.erofs is an absolute symlink into the operator-owned cache blob.
	link := s.layerBlobPath(snapshotID(t, ctx, s, key))
	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "layer.erofs should be a symlink")
	dst, err := os.Readlink(link)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dst), "symlink target should be absolute")
	assert.Equal(t, blob, dst)

	// Commit finalizes the staged snapshot as the target chainID, without any
	// re-conversion (the blob is already present).
	require.NoError(t, s.Commit(ctx, target, key))
	info, err := s.Stat(ctx, target)
	require.NoError(t, err, "committed snapshot must exist under the target chainID")
	assert.Equal(t, snapshots.KindCommitted, info.Kind)
	assert.Equal(t, "", info.Parent)
}

// TestCacheSidecar covers a hit in the default "auto" dm-verity mode where the
// cache entry has a sidecar: it must be copied into the snapshot dir as a plain
// regular file (not symlinked).
func TestCacheSidecar(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	blob := writeCacheBlob(t, cacheDir, diffID, []byte("fake erofs blob"))
	require.NoError(t, os.WriteFile(dmverity.MetadataPath(blob), []byte(testDmverityMetadata), 0644))

	// dmverity_mode defaults to "auto": use the sidecar if present.
	s := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	target := cacheTestChainID
	key := stageCacheHit(t, ctx, s, target, diffID)

	// The sidecar is copied in as a plain regular file (not a symlink) so mount-time
	// metadata resolution is independent of the cache filesystem.
	sidecar := dmverity.MetadataPath(s.layerBlobPath(snapshotID(t, ctx, s, key)))
	fi, err := os.Lstat(sidecar)
	require.NoError(t, err, "sidecar should be copied into the snapshot dir")
	assert.Zero(t, fi.Mode()&os.ModeSymlink, "sidecar should be a regular file, not a symlink")
	data, err := os.ReadFile(sidecar)
	require.NoError(t, err)
	assert.Equal(t, testDmverityMetadata, string(data))
}

// TestCacheMiss covers the cases that must NOT be served from the cache and
// instead create a normal active snapshot: cache disabled, blob absent, a
// label-less (container-rootfs) Prepare, and a View (which the KindActive gate
// excludes even with matching labels and a cached blob).
func TestCacheMiss(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID

	// Each case must leave the extraction as a normal active snapshot: mounts are
	// returned and the target chainID is not committed. wantRO distinguishes a
	// KindActive miss (no layer.erofs staged yet, so mounts must be writable)
	// from the KindView case below (read-only for its own, cache-unrelated reason).
	assertFellThrough := func(t *testing.T, s *snapshotter, mounts []mount.Mount, err error, wantRO bool) {
		t.Helper()
		require.NoError(t, err)
		assert.NotEmpty(t, mounts, "a miss must return normal active-snapshot mounts")
		for _, m := range mounts {
			assert.Equal(t, wantRO, m.ReadOnly())
		}
		_, err = s.Stat(ctx, target)
		assert.Error(t, err, "target chainID must not be committed on a miss")
	}

	t.Run("cache disabled", func(t *testing.T) {
		s := newCacheSnapshotter(t) // no cache configured
		mounts, err := s.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		assertFellThrough(t, s, mounts, err, false)
	})

	t.Run("blob absent", func(t *testing.T) {
		s := newCacheSnapshotter(t, WithLayerContentCaches(t.TempDir()))
		mounts, err := s.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		assertFellThrough(t, s, mounts, err, false)
	})

	t.Run("no extraction labels", func(t *testing.T) {
		cacheDir := t.TempDir()
		writeCacheBlob(t, cacheDir, diffID, []byte("blob"))
		s := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))
		// A container-rootfs Prepare carries no snapshot.ref/diff-id labels.
		mounts, err := s.Prepare(ctx, "container-rootfs", "")
		require.NoError(t, err)
		assert.NotEmpty(t, mounts)
	})

	t.Run("view is never short-circuited", func(t *testing.T) {
		cacheDir := t.TempDir()
		writeCacheBlob(t, cacheDir, diffID, []byte("blob"))
		s := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))
		// Even with matching labels and a cached blob, a View must not commit.
		// It's read-only, but via the KindView roFlag in mounts(), not the cache.
		mounts, err := s.View(ctx, "view-1", "", extractionOpt(target, diffID))
		assertFellThrough(t, s, mounts, err, true)
	})
}

// TestCacheParentedPrepare covers a cached blob whose extraction Prepare carries
// a parent (the sequential unpack path): it must not be staged. mounts() only
// picks a staged blob up when the snapshot has no parents, so otherwise the
// unpacker would see a writable overlay, apply the layer, and let the differ
// write through the symlink into the shared cache blob.
func TestCacheParentedPrepare(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	var (
		cacheDir     = t.TempDir()
		parentDiffID = digest.Digest(cacheTestDiffID)
		parentChain  = cacheTestChainID
		childDiffID  = digest.Digest("sha256:0000000000000000000000000000000000000000000000000000000000000003")
		childChain   = "sha256:0000000000000000000000000000000000000000000000000000000000000004"
	)
	writeCacheBlob(t, cacheDir, parentDiffID, []byte("fake parent blob"))
	writeCacheBlob(t, cacheDir, childDiffID, []byte("fake child blob"))

	s := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	// The first layer has no parent, so it is served from the cache as usual.
	require.NoError(t, s.Commit(ctx, parentChain, stageCacheHit(t, ctx, s, parentChain, parentDiffID)))

	key := "extract-1 " + childChain
	mounts, err := s.Prepare(ctx, key, parentChain, extractionOpt(childChain, childDiffID))
	require.NoError(t, err)
	require.NotEmpty(t, mounts)
	// The unpacker only inspects the last mount, which must be the writable
	// overlay so the layer gets applied (the parents' lowers are read-only).
	assert.False(t, mounts[len(mounts)-1].ReadOnly(), "a parented Prepare must expose a writable overlay")

	// Nothing was staged, so the differ has no symlink to write through.
	_, err = os.Lstat(s.layerBlobPath(snapshotID(t, ctx, s, key)))
	assert.ErrorIs(t, err, os.ErrNotExist, "no cached blob may be staged into a parented snapshot")
}

// TestCacheRemove covers removal of a cache-hit snapshot: it succeeds (the
// setImmutable guard skips the symlink), removes the snapshot dir/symlink, and
// leaves the operator-owned cache blob and sidecar untouched.
func TestCacheRemove(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	blob := writeCacheBlob(t, cacheDir, diffID, []byte("fake erofs blob"))
	sidecar := dmverity.MetadataPath(blob)
	require.NoError(t, os.WriteFile(sidecar, []byte(testDmverityMetadata), 0644))

	s := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	target := cacheTestChainID
	key := stageCacheHit(t, ctx, s, target, diffID)
	require.NoError(t, s.Commit(ctx, target, key))

	snapDir := filepath.Dir(s.layerBlobPath(snapshotID(t, ctx, s, target)))

	// Remove must succeed (the symlinked blob is skipped by the setImmutable guard,
	// which would otherwise follow the link and ioctl the operator-owned blob).
	require.NoError(t, s.Remove(ctx, target))

	// The snapshot dir (and its symlink) is gone, but the cache is untouched.
	_, err := os.Stat(snapDir)
	assert.True(t, os.IsNotExist(err), "snapshot dir should be removed")
	_, err = os.Stat(blob)
	require.NoError(t, err, "cache blob must be untouched by Remove")
	_, err = os.Stat(sidecar)
	require.NoError(t, err, "cache sidecar must be untouched by Remove")
}

// TestCacheDmverity covers dmverity_mode="on": a cache entry with a sidecar is
// staged (and the sidecar copied), while an entry missing its required sidecar is
// a hard error (not staged, nothing committed).
func TestCacheDmverity(t *testing.T) {
	if supported, err := dmverity.IsSupported(); err != nil || !supported {
		t.Skip("dm-verity is not supported on this system")
	}
	ctx := namespaces.WithNamespace(context.Background(), "test")
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID

	t.Run("with sidecar stages and copies it", func(t *testing.T) {
		cacheDir := t.TempDir()
		blob := writeCacheBlob(t, cacheDir, diffID, []byte("fake erofs blob"))
		require.NoError(t, os.WriteFile(dmverity.MetadataPath(blob), []byte(testDmverityMetadata), 0644))

		s := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir), WithDmverityMode("on"))
		key := stageCacheHit(t, ctx, s, target, diffID)

		_, err := os.Stat(dmverity.MetadataPath(s.layerBlobPath(snapshotID(t, ctx, s, key))))
		require.NoError(t, err, "sidecar must be present for a dmverity_mode=on hit")
	})

	t.Run("without sidecar fails the pull", func(t *testing.T) {
		cacheDir := t.TempDir()
		writeCacheBlob(t, cacheDir, diffID, []byte("fake erofs blob")) // no sidecar

		s := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir), WithDmverityMode("on"))

		// dmverity_mode=on requires a sidecar; a cache entry without one is a hard
		// error rather than a silent fallback.
		mounts, err := s.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		require.Error(t, err)
		assert.Nil(t, mounts, "missing sidecar must not be treated as a hit")
		_, err = s.Stat(ctx, target)
		assert.Error(t, err, "no snapshot should be committed on failure")
	})
}

// TestCacheMultipleDirs covers the layer_content_caches search path: directories
// are searched in configured order, the first hit wins, later caches are reached
// when earlier ones lack the blob, and a miss in every cache falls through to a
// normal writable snapshot.
func TestCacheMultipleDirs(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID

	// stagedBlob returns the cache blob the snapshot's layer.erofs symlink points
	// at, i.e. which of the configured caches actually served the layer.
	stagedBlob := func(t *testing.T, s *snapshotter, key string) string {
		t.Helper()
		dst, err := os.Readlink(s.layerBlobPath(snapshotID(t, ctx, s, key)))
		require.NoError(t, err)
		return dst
	}

	t.Run("first cache with the blob wins", func(t *testing.T) {
		first, second := t.TempDir(), t.TempDir()
		// Both caches hold the diffID; the earlier one must be the one used.
		firstBlob := writeCacheBlob(t, first, diffID, []byte("from first"))
		writeCacheBlob(t, second, diffID, []byte("from second"))

		s := newCacheSnapshotter(t, WithLayerContentCaches(first, second))
		key := stageCacheHit(t, ctx, s, target, diffID)
		assert.Equal(t, firstBlob, stagedBlob(t, s, key))
	})

	t.Run("falls through to a later cache", func(t *testing.T) {
		empty, populated := t.TempDir(), t.TempDir()
		blob := writeCacheBlob(t, populated, diffID, []byte("from second"))

		s := newCacheSnapshotter(t, WithLayerContentCaches(empty, populated))
		key := stageCacheHit(t, ctx, s, target, diffID)
		assert.Equal(t, blob, stagedBlob(t, s, key))
	})

	t.Run("miss in every cache falls back to a writable snapshot", func(t *testing.T) {
		s := newCacheSnapshotter(t, WithLayerContentCaches(
			filepath.Join(t.TempDir(), "missing"), t.TempDir(), t.TempDir()))

		mounts, err := s.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		require.NoError(t, err)
		require.NotEmpty(t, mounts)
		for _, m := range mounts {
			assert.False(t, m.ReadOnly(), "a miss in every cache must return writable mounts")
		}
		_, err = s.Stat(ctx, target)
		assert.Error(t, err, "target chainID must not be committed on a miss")
	})
}

// TestCacheDirMustBeAbsolute covers the one thing NewSnapshotter checks about a
// configured cache dir: it must be absolute, since a relative one would be
// symlinked into the snapshot dir and dangle. The empty string is covered by the
// same check, which otherwise resolves to the daemon's working directory.
func TestCacheDirMustBeAbsolute(t *testing.T) {
	requireErofs(t)

	for _, dir := range []string{"relative-cache", "./cache", "", "a/b"} {
		t.Run(fmt.Sprintf("%q", dir), func(t *testing.T) {
			_, err := NewSnapshotter(t.TempDir(), WithLayerContentCaches(dir))
			assert.ErrorContains(t, err, "must be an absolute path")
		})
	}
}
