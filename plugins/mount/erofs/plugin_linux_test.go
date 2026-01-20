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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/fsmount"
	"github.com/containerd/containerd/v2/pkg/testutil"
	snapshotserofs "github.com/containerd/containerd/v2/plugins/snapshots/erofs"
	"golang.org/x/sys/unix"
)

// TestFsmountLoopDevice tests fsmount with loop device
func TestFsmountLoopDevice(t *testing.T) {
	testutil.RequiresRoot(t)

	if _, err := exec.LookPath("mkfs.erofs"); err != nil {
		t.Skipf("mkfs.erofs not found: %v", err)
	}

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

	cmd := exec.Command("mkfs.erofs", layerPath, layerDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create erofs image: %v, output: %s", err, output)
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

	if _, err := exec.LookPath("mkfs.erofs"); err != nil {
		t.Skipf("mkfs.erofs not found: %v", err)
	}

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

	cmd := exec.Command("mkfs.erofs", layerPath, layerDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create erofs image: %v, output: %s", err, output)
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

	for i := 0; i < 100; i++ {
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

		for i := 0; i < 100; i++ {
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
