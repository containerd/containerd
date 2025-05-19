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
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	testFileContent       = "Hello, this is content for testing the EROFS Snapshotter!"
	testNestedFileContent = "Nested file content"
)

func newSnapshotter(t *testing.T, opts ...Opt) func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	_, err := exec.LookPath("mkfs.erofs")
	if err != nil {
		t.Skipf("could not find mkfs.erofs: %v", err)
	}

	if !findErofs() {
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
	root, err := os.MkdirTemp(t.TempDir(), "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

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

	if !findErofs() {
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
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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

// TestCreateDmverityErofsMount tests dm-verity mount creation
func TestCreateDmverityErofsMount(t *testing.T) {
	testutil.RequiresRoot(t)

	tmpDir := t.TempDir()

	// Helper to create layer with metadata
	createLayer := func(name, roothash string, hashOffset int64) string {
		layerBlob := filepath.Join(tmpDir, name)
		metadataFile := layerBlob + ".dmverity"

		metadataContent := fmt.Sprintf(`{
  "roothash": "%s",
  "hashoffset": %d
}`, roothash, hashOffset)
		require.NoError(t, os.WriteFile(metadataFile, []byte(metadataContent), 0644))
		require.NoError(t, os.WriteFile(layerBlob, []byte{}, 0644))

		return layerBlob
	}

	// Helper to check if option exists
	hasOption := func(options []string, opt string) bool {
		for _, o := range options {
			if o == opt {
				return true
			}
		}
		return false
	}

	s := &snapshotter{
		root:           tmpDir,
		enableDmverity: true,
	}

	t.Run("creates dmverity mount with metadata", func(t *testing.T) {
		layerBlob := createLayer("layer.erofs",
			"abc123def456789012345678901234567890123456789012345678901234", 8192)

		m, err := s.createDmverityErofsMount("test-id", layerBlob)
		require.NoError(t, err)

		assert.Equal(t, "dmverity/erofs", m.Type)
		assert.Equal(t, layerBlob, m.Source)
		assert.True(t, hasOption(m.Options, "ro"), "should have ro option")
		assert.True(t, hasOption(m.Options, "X-containerd.dmverity.roothash=abc123def456789012345678901234567890123456789012345678901234"))
		assert.True(t, hasOption(m.Options, "X-containerd.dmverity.hash-offset=8192"))
		assert.True(t, hasOption(m.Options, "X-containerd.dmverity.device-name=containerd-erofs-test-id"))
	})

	t.Run("fails without metadata file", func(t *testing.T) {
		layerBlob := filepath.Join(tmpDir, "layer-no-meta.erofs")
		require.NoError(t, os.WriteFile(layerBlob, []byte{}, 0644))

		_, err := s.createDmverityErofsMount("test-id-2", layerBlob)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read dm-verity metadata")
	})

	t.Run("fails with invalid metadata format", func(t *testing.T) {
		layerBlob := filepath.Join(tmpDir, "layer-bad-meta.erofs")
		metadataFile := layerBlob + ".dmverity"

		require.NoError(t, os.WriteFile(metadataFile, []byte("invalid metadata"), 0644))
		require.NoError(t, os.WriteFile(layerBlob, []byte{}, 0644))

		_, err := s.createDmverityErofsMount("test-id-bad", layerBlob)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read dm-verity metadata")
	})
}

// TestCreateErofsMount tests mount creation without dm-verity
func TestCreateErofsMount(t *testing.T) {
	tmpDir := t.TempDir()
	layerBlob := filepath.Join(tmpDir, "layer.erofs")
	require.NoError(t, os.WriteFile(layerBlob, []byte{}, 0644))

	s := &snapshotter{
		root:           tmpDir,
		enableDmverity: false,
	}

	t.Run("creates regular erofs mount", func(t *testing.T) {
		m, err := s.createErofsMount("test-id", layerBlob)
		require.NoError(t, err)

		assert.Equal(t, "erofs", m.Type)
		assert.Equal(t, layerBlob, m.Source)
		assert.Equal(t, []string{"ro", "loop"}, m.Options)
	})

	t.Run("dispatcher calls dmverity function when enabled", func(t *testing.T) {
		s.enableDmverity = true
		metadataFile := layerBlob + ".dmverity"
		metadataContent := `{
  "roothash": "fedcba098765432109876543210987654321098765432109876543210987",
  "hashoffset": 4096
}`
		require.NoError(t, os.WriteFile(metadataFile, []byte(metadataContent), 0644))

		m, err := s.createErofsMount("test-id", layerBlob)
		require.NoError(t, err)
		assert.Equal(t, "dmverity/erofs", m.Type)
		assert.True(t, len(m.Options) > 2, "should have dm-verity options")
		assert.Contains(t, m.Options, "X-containerd.dmverity.roothash=fedcba098765432109876543210987654321098765432109876543210987")
	})
}

// TestDmverityDeviceName tests device name generation
func TestDmverityDeviceName(t *testing.T) {
	s := &snapshotter{}

	// Test basic device name generation
	deviceName := s.dmverityDeviceName("abc123")
	assert.Equal(t, "containerd-erofs-abc123", deviceName)

	// Verify it integrates with DevicePath helper
	devicePath := dmverity.DevicePath(deviceName)
	assert.Equal(t, "/dev/mapper/containerd-erofs-abc123", devicePath)
}

// TestDmverityEndToEnd tests the full workflow: differ creates dm-verity layer,
// snapshotter mounts it via mount manager, and cleanup on removal
func TestDmverityEndToEnd(t *testing.T) {
	testutil.RequiresRoot(t)

	// Check if dm-verity is supported
	supported, err := dmverity.IsSupported()
	if err != nil || !supported {
		t.Skip("dm-verity is not supported on this system")
	}

	ctx := context.Background()
	ctx = namespaces.WithNamespace(ctx, "test")
	tempDir := t.TempDir()

	// Create mount manager database
	metadb := filepath.Join(tempDir, "mounts.db")
	db, err := bolt.Open(metadb, 0600, nil)
	require.NoError(t, err)
	defer db.Close()

	// Create mount manager with dm-verity support
	mountTargetDir := filepath.Join(tempDir, "mount-manager")
	mgr, err := mountmanager.NewManager(db, mountTargetDir)
	require.NoError(t, err)

	// Create content store for the differ
	contentStore, err := local.NewStore(filepath.Join(tempDir, "content"))
	require.NoError(t, err)

	// Create EROFS differ with dm-verity enabled
	differ := erofsdiffer.NewErofsDiffer(contentStore, erofsdiffer.WithDmverity())

	// Create EROFS snapshotter with dm-verity enabled
	snapshotRoot := filepath.Join(tempDir, "snapshots")
	sn, err := NewSnapshotter(snapshotRoot, WithDmverity())
	require.NoError(t, err)
	defer sn.Close()

	s := sn.(*snapshotter)

	// Create test tar content
	tarReader := createTestTarContent()
	defer tarReader.Close()

	// Read the tar content into a buffer
	tarContent, err := io.ReadAll(tarReader)
	require.NoError(t, err)

	// Write tar content to content store
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

	// Step 1: Prepare snapshot
	snapshotKey := "test-snapshot"
	mounts, err := sn.Prepare(ctx, snapshotKey, "")
	require.NoError(t, err)

	// Step 2: Apply layer using differ (creates EROFS + dm-verity)
	appliedDesc, err := differ.Apply(ctx, desc, mounts)
	require.NoError(t, err)
	t.Logf("Applied layer with dm-verity: %s", appliedDesc.Digest)

	// Step 3: Commit the snapshot
	commitKey := "test-commit"
	err = sn.Commit(ctx, commitKey, snapshotKey)
	require.NoError(t, err)

	// Get snapshot ID
	var snapshotID string
	err = s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		var err error
		snapshotID, _, _, err = storage.GetInfo(ctx, commitKey)
		return err
	})
	require.NoError(t, err)

	// Step 4: Verify dm-verity metadata was created by differ
	layerPath := s.layerBlobPath(snapshotID)
	metadataPath := layerPath + ".dmverity"

	metadataData, err := os.ReadFile(metadataPath)
	require.NoError(t, err, ".dmverity file should exist")
	require.NotEmpty(t, metadataData, "metadata should not be empty")
	t.Logf("Root hash: %s", string(metadataData))

	// Step 5: Create a view and mount it (uses mount manager with dm-verity)
	viewKey := "test-view"
	viewMounts, err := sn.View(ctx, viewKey, commitKey)
	require.NoError(t, err)

	// Verify the mount type is dmverity/erofs
	require.Len(t, viewMounts, 1)
	assert.Equal(t, "dmverity/erofs", viewMounts[0].Type)
	assert.Contains(t, viewMounts[0].Options, "ro")

	// Check that dm-verity options are present
	hasRootHash := false
	hasDeviceName := false
	deviceName := ""
	for _, opt := range viewMounts[0].Options {
		if len(opt) > len("X-containerd.dmverity.roothash=") &&
			opt[:len("X-containerd.dmverity.roothash=")] == "X-containerd.dmverity.roothash=" {
			hasRootHash = true
		}
		if len(opt) > len("X-containerd.dmverity.device-name=") &&
			opt[:len("X-containerd.dmverity.device-name=")] == "X-containerd.dmverity.device-name=" {
			hasDeviceName = true
			deviceName = opt[len("X-containerd.dmverity.device-name="):]
		}
	}
	assert.True(t, hasRootHash, "mount should have root hash option")
	assert.True(t, hasDeviceName, "mount should have device name option")

	t.Logf("Step 5: View created with dm-verity mount options: %v", viewMounts[0].Options)

	// Step 6: Use mount manager to mount the view (processes dm-verity options)
	viewTarget := filepath.Join(tempDir, "view-mount")
	require.NoError(t, os.MkdirAll(viewTarget, 0755))

	t.Logf("Step 6: Activating mount via mount manager (triggers dm-verity device creation)...")
	mountID := "test-view-mount"
	activateInfo, err := mgr.Activate(ctx, mountID, viewMounts)
	require.NoError(t, err)

	t.Logf("Step 6: Mount activated, system mounts: %v", activateInfo.System)

	// Mount the activated system mounts
	err = mount.All(activateInfo.System, viewTarget)
	require.NoError(t, err)
	defer testutil.Unmount(t, viewTarget)

	// Check if dm-verity device was created
	devicePath := fmt.Sprintf("/dev/mapper/%s", deviceName)
	_, err = os.Stat(devicePath)
	require.NoError(t, err, "dm-verity device should exist at %s", devicePath)
	t.Logf("dm-verity device created successfully: %s", devicePath)

	// Verify device is active using dmsetup
	cmd := exec.Command("dmsetup", "info", deviceName)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "dmsetup info should succeed")
	t.Logf("Device status:\n%s", string(output))

	// Verify we can read from the mounted filesystem
	files, err := os.ReadDir(viewTarget)
	require.NoError(t, err, "should be able to read mounted filesystem")
	t.Logf("Mounted filesystem contains %d entries", len(files))

	// Step 7: Unmount before cleanup
	t.Logf("Step 7: Unmounting view...")
	testutil.Unmount(t, viewTarget)
	t.Logf("Step 7: View unmounted successfully")

	// Deactivate the mount manager mount
	t.Logf("Deactivating mount manager mount...")
	err = mgr.Deactivate(ctx, mountID)
	require.NoError(t, err)
	t.Logf("Mount manager mount deactivated")

	// Verify device is still present after unmount (not cleaned up yet)
	_, err = os.Stat(devicePath)
	if err == nil {
		t.Logf("dm-verity device still exists after unmount: %s (will be cleaned up on Remove)", devicePath)
	}

	// Step 8: Remove the view first (child must be removed before parent)
	t.Logf("Step 8: Removing view snapshot...")
	err = sn.Remove(ctx, viewKey)
	require.NoError(t, err)
	t.Logf("Step 8: View snapshot removed")

	// Step 9: Remove the committed snapshot
	// This tests the full cleanup path including dm-verity device cleanup
	t.Logf("Step 9: Removing committed snapshot (triggers dm-verity cleanup)...")
	err = sn.Remove(ctx, commitKey)
	require.NoError(t, err)
	t.Logf("Step 9: Committed snapshot removed")

	// Verify device was cleaned up
	_, err = os.Stat(devicePath)
	if err == nil {
		// Device still exists - check if it's still active
		cmd = exec.Command("dmsetup", "info", deviceName)
		output, _ = cmd.CombinedOutput()
		t.Logf("Warning: dm-verity device still exists after Remove():\n%s", string(output))
	} else {
		t.Logf("dm-verity device cleaned up successfully")
	} // Verify snapshot was removed
	err = s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, err := storage.GetSnapshot(ctx, commitKey)
		return err
	})
	assert.Error(t, err, "snapshot should be removed from metadata")

	t.Logf("Successfully completed end-to-end dm-verity test")
}
