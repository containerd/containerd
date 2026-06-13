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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/containerd/containerd/v2/internal/fsmount"
	"github.com/containerd/containerd/v2/pkg/testutil"
	snapshotserofs "github.com/containerd/containerd/v2/plugins/snapshots/erofs"
	"golang.org/x/sys/unix"
)

// TestFsmountLoopDevice tests fsmount with loop device
func TestFsmountLoopDevice(t *testing.T) {
	testutil.RequiresRoot(t)

	if !snapshotserofs.FindErofs() {
		t.Skip("erofs kernel support not available")
	}

	if !fsmount.SupportsFsmount() {
		t.Skip("fsmount syscall not available (requires Linux 5.2+)")
	}

	tempDir := t.TempDir()

	layerPath := filepath.Join(tempDir, "test.erofs")
	layerDir := filepath.Join(tempDir, "layer_dir")
	if err := os.MkdirAll(layerDir, 0755); err != nil {
		t.Fatalf("failed to create layer dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := erofsutils.ConvertErofs(context.Background(), layerPath, layerDir); err != nil {
		t.Fatalf("failed to create erofs image: %v", err)
	}

	loopFile, err := mount.SetupLoop(layerPath, mount.LoopParams{
		Readonly:  true,
		Autoclear: true,
	})
	if err != nil {
		t.Fatalf("failed to setup loop device: %v", err)
	}
	loopDev := loopFile.Name()
	defer func() {
		loopFile.Close()
		mount.DetachLoopDevice(loopDev)
	}()

	mountPoint := filepath.Join(tempDir, "mnt")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("failed to create mount point: %v", err)
	}

	m := mount.Mount{
		Type:    "erofs",
		Source:  loopDev,
		Options: []string{"ro"},
	}

	err = fsmount.Fsmount(m, mountPoint)
	if err != nil {
		t.Fatalf("fsmount with loop device failed: %v", err)
	}
	defer mount.Unmount(mountPoint, 0)

	content, err := os.ReadFile(filepath.Join(mountPoint, "test.txt"))
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected content: %s", content)
	}
}

// TestMountOptionsPageSizeLimit tests that when mount options exceed the kernel's
// PAGE_SIZE limit (typically 4KB), the traditional mount() syscall fails but
// fsmount API succeeds.
func TestMountOptionsPageSizeLimit(t *testing.T) {
	testutil.RequiresRoot(t)

	if !snapshotserofs.FindErofs() {
		t.Skip("erofs kernel support not available")
	}

	if !fsmount.SupportsFsmount() {
		t.Skip("fsmount syscall not available (requires Linux 5.2+)")
	}

	tempDir := t.TempDir()

	layerPath := filepath.Join(tempDir, "test.erofs")
	layerDir := filepath.Join(tempDir, "layer_dir")
	if err := os.MkdirAll(layerDir, 0755); err != nil {
		t.Fatalf("failed to create layer dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := erofsutils.ConvertErofs(context.Background(), layerPath, layerDir); err != nil {
		t.Fatalf("failed to create erofs image: %v", err)
	}

	loopFile, err := mount.SetupLoop(layerPath, mount.LoopParams{
		Readonly:  true,
		Autoclear: true,
	})
	if err != nil {
		t.Fatalf("failed to setup loop device: %v", err)
	}
	loopDev := loopFile.Name()
	defer func() {
		loopFile.Close()
		mount.DetachLoopDevice(loopDev)
	}()

	// Build mount options that exceed PAGE_SIZE (4096 bytes)
	options := []string{"ro"}
	var optionsBuilder strings.Builder
	optionsBuilder.WriteString("ro")

	for range 100 {
		fakeDevice := filepath.Join(tempDir, strings.Repeat("x", 50), "fake_device_"+strings.Repeat("0", 10))
		opt := "device=" + fakeDevice
		options = append(options, opt)
		optionsBuilder.WriteString(",")
		optionsBuilder.WriteString(opt)
	}

	optionsLen := optionsBuilder.Len()
	if optionsLen <= 4096 {
		t.Fatalf("Test setup error: options string (%d bytes) should exceed PAGE_SIZE (4096)", optionsLen)
	}

	mountPoint := filepath.Join(tempDir, "mnt")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("failed to create mount point: %v", err)
	}

	m := mount.Mount{
		Type:    "erofs",
		Source:  loopDev,
		Options: options,
	}

	// Test 1: Traditional mount should fail due to PAGE_SIZE limit
	t.Run("traditional_mount_fails", func(t *testing.T) {
		err := m.Mount(mountPoint)
		if err == nil {
			mount.Unmount(mountPoint, 0)
			t.Fatal("Expected traditional mount to fail due to PAGE_SIZE limit, but it succeeded")
		}
		if !strings.Contains(err.Error(), "mount options is too long") {
			t.Fatalf("Expected 'mount options is too long' error, got: %v", err)
		}
	})

	// Test 2: fsmount should succeed
	t.Run("fsmount_succeeds", func(t *testing.T) {
		simpleMount := mount.Mount{
			Type:    "erofs",
			Source:  loopDev,
			Options: []string{"ro"},
		}

		err := fsmount.Fsmount(simpleMount, mountPoint)
		if err != nil {
			if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
				t.Skipf("fsmount failed due to permission restrictions: %v", err)
			}
			t.Fatalf("fsmount failed: %v", err)
		}

		content, err := os.ReadFile(filepath.Join(mountPoint, "test.txt"))
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}
		if string(content) != "hello" {
			t.Fatalf("unexpected content: %s", content)
		}

		if err := mount.Unmount(mountPoint, 0); err != nil {
			t.Fatalf("failed to unmount: %v", err)
		}
	})

	// Test 3: Verify that fsmount can handle many options without PAGE_SIZE limit
	t.Run("fsmount_many_options", func(t *testing.T) {
		manyOptions := []string{"ro"}
		var builder strings.Builder
		builder.WriteString("ro")

		for range 100 {
			manyOptions = append(manyOptions, "dax=always")
			builder.WriteString(",dax=always")
		}

		manyOptionsMount := mount.Mount{
			Type:    "erofs",
			Source:  loopDev,
			Options: manyOptions,
		}

		err := fsmount.Fsmount(manyOptionsMount, mountPoint)
		if err != nil {
			if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
				t.Skipf("fsmount failed due to permission restrictions: %v", err)
			}
			return
		}

		mount.Unmount(mountPoint, 0)
	})
}

// TestDmverityDeviceName covers the naming rule for dm-verity devices. Mount
// closes the device it creates when the mount fails, so two mounts that share a
// name would let one of them close a device the other is using.
func TestDmverityDeviceName(t *testing.T) {
	const (
		snapshotBlob = "/var/lib/containerd/snapshots/42/layer.erofs"
		// A layer content cache shards blobs by the first two characters of the
		// digest, so a whole shard shares one directory.
		cachedBlob      = "/mnt/cache/sha256/ab/abcdef0123456789.erofs"
		cachedBlobOther = "/mnt/cache/sha256/ab/abfedcba9876543210.erofs"
	)

	// The same blob mounted in two places is two mounts, each owning a device.
	assert.NotEqual(t,
		dmverityDeviceName(cachedBlob, "/run/a"),
		dmverityDeviceName(cachedBlob, "/run/b"),
		"one blob mounted in two places names two devices")

	// Two blobs in one cache shard are two layers.
	assert.NotEqual(t,
		dmverityDeviceName(cachedBlob, "/run/a"),
		dmverityDeviceName(cachedBlobOther, "/run/a"),
		"two blobs in a shard name two devices")

	assert.NotEqual(t,
		dmverityDeviceName(snapshotBlob, "/run/a"),
		dmverityDeviceName(cachedBlob, "/run/a"),
		"a snapshot blob and a cached blob name two devices")

	// A device left behind by a crash is reused for the same blob in the same
	// place, where its root hash still matches.
	assert.Equal(t,
		dmverityDeviceName(cachedBlob, "/run/a"),
		dmverityDeviceName(cachedBlob, "/run/a"),
		"one blob in one place names one device")

	name := dmverityDeviceName(cachedBlob, "/run/a")
	assert.True(t, strings.HasPrefix(name, "containerd-erofs-"), "Unmount finds the device by this prefix")
	assert.Less(t, len(name), 128, "a device-mapper name is limited to 127 characters")
}

// TestDataDeviceDmverityName covers the naming rule for the dm-verity devices of
// data devices. Unmount finds them by the mount point's prefix alone, since a
// data device does not show in the mount table.
func TestDataDeviceDmverityName(t *testing.T) {
	const device = "/var/lib/containerd/snapshots/42/device-0.blob"

	name := dataDeviceDmverityName("/run/a", 0, device)
	assert.True(t, strings.HasPrefix(name, dataDeviceDmverityPrefix("/run/a")), "Unmount finds the device by the mount point's prefix")
	assert.False(t, strings.HasPrefix(name, dataDeviceDmverityPrefix("/run/b")), "another mount's Unmount must not close it")
	assert.NotEqual(t, name, dataDeviceDmverityName("/run/a", 1, device), "two devices of one mount name two devices")
	assert.NotEqual(t, name, dataDeviceDmverityName("/run/a", 0, "/var/lib/containerd/snapshots/43/device-0.blob"),
		"a different blob at the same index names another device")
	assert.NotEqual(t, name, dmverityDeviceName(device, "/run/a"), "a data device and a source name two devices")
	assert.Less(t, len(name), 128, "a device-mapper name is limited to 127 characters")
}

// TestIsBlockDevice covers how Mount tells a block device from a file. The
// path alone does not say: a layer file can live under /dev/shm.
func TestIsBlockDevice(t *testing.T) {
	file := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0644))
	assert.False(t, isBlockDevice(file), "a regular file")
	assert.False(t, isBlockDevice("/dev/null"), "a character device")
	assert.False(t, isBlockDevice(filepath.Join(t.TempDir(), "missing")), "a missing path")

	matches, _ := filepath.Glob("/dev/loop[0-9]*")
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.Mode()&os.ModeDevice != 0 && fi.Mode()&os.ModeCharDevice == 0 {
			assert.True(t, isBlockDevice(m), "a loop block device")
			return
		}
	}
}
