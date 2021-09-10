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

package os

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Microsoft/go-winio/vhd"
	"github.com/Microsoft/hcsshim/computestorage"
	"github.com/Microsoft/hcsshim/osversion"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Getting build number via osversion.Build() requires the program to be properly manifested, which
// requires importing `github.com/Microsoft/hcsshim/test/functional/manifest`, which is a dependency
// we want to avoid here. Instead, since this is just test code, simply parse the build number from
// the registry.
func getWindowsBuildNumber() (int, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return 0, fmt.Errorf("read CurrentVersion reg key: %s", err)
	}
	defer k.Close()
	buildNumStr, _, err := k.GetStringValue("CurrentBuild")
	if err != nil {
		return 0, fmt.Errorf("read CurrentBuild reg value: %s", err)
	}
	buildNum, err := strconv.Atoi(buildNumStr)
	if err != nil {
		return 0, err
	}
	return buildNum, nil
}

func makeSymlink(t *testing.T, oldName string, newName string) {
	if err := os.Symlink(oldName, newName); err != nil {
		t.Fatalf("creating symlink: %s", err)
	}
}

func getVolumeGUIDPath(t *testing.T, path string) string {
	h, err := openPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	final, err := getFinalPathNameByHandle(h, cFILE_NAME_OPENED|cVOLUME_NAME_GUID)
	if err != nil {
		t.Fatal(err)
	}
	return final
}

func openDisk(path string) (windows.Handle, error) {
	u16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(
		u16,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_NO_BUFFERING,
		0)
	if err != nil {
		return 0, &os.PathError{
			Op:   "CreateFile",
			Path: path,
			Err:  err,
		}
	}
	return h, nil
}

func formatVHD(vhdHandle windows.Handle) error {
	h := vhdHandle
	// Pre-19H1 HcsFormatWritableLayerVhd expects a disk handle.
	// On newer builds it expects a VHD handle instead.
	// Open a handle to the VHD's disk object if needed.
	build, err := getWindowsBuildNumber()
	if err != nil {
		return err
	}
	if build < osversion.V19H1 {
		diskPath, err := vhd.GetVirtualDiskPhysicalPath(syscall.Handle(h))
		if err != nil {
			return err
		}
		diskHandle, err := openDisk(diskPath)
		if err != nil {
			return err
		}
		defer windows.CloseHandle(diskHandle)
		h = diskHandle
	}
	// Formatting a disk directly in Windows is a pain, so we use FormatWritableLayerVhd to do it.
	// It has a side effect of creating a sandbox directory on the formatted volume, but it's safe
	// to just ignore that for our purposes here.
	if err := computestorage.FormatWritableLayerVhd(context.Background(), h); err != nil {
		return err
	}
	return nil
}

// Creates a VHD with a NTFS volume. Returns the volume path.
func setupVHDVolume(t *testing.T, vhdPath string) string {
	vhdHandle, err := vhd.CreateVirtualDisk(vhdPath, vhd.VirtualDiskAccessNone, vhd.CreateVirtualDiskFlagNone, &vhd.CreateVirtualDiskParameters{
		Version: 2,
		Version2: vhd.CreateVersion2{
			MaximumSize:      5 * 1024 * 1024 * 1024, // 5GB, thin provisioned
			BlockSizeInBytes: 1 * 1024 * 1024,        // 1MB
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		windows.CloseHandle(windows.Handle(vhdHandle))
	})
	if err := vhd.AttachVirtualDisk(vhdHandle, vhd.AttachVirtualDiskFlagNone, &vhd.AttachVirtualDiskParameters{Version: 1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := vhd.DetachVirtualDisk(vhdHandle); err != nil {
			t.Fatal(err)
		}
	})
	if err := formatVHD(windows.Handle(vhdHandle)); err != nil {
		t.Fatalf("failed to format VHD: %s", err)
	}
	// Get the path for the volume that was just created on the disk.
	volumePath, err := computestorage.GetLayerVhdMountPath(context.Background(), windows.Handle(vhdHandle))
	if err != nil {
		t.Fatal(err)
	}
	return volumePath
}

func writeFile(t *testing.T, path string, content []byte) {
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
}

func mountVolume(t *testing.T, volumePath string, mountPoint string) {
	// Create the mount point directory.
	if err := os.Mkdir(mountPoint, 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(mountPoint); err != nil {
			t.Fatal(err)
		}
	})
	// Volume path must end in a slash.
	if !strings.HasSuffix(volumePath, `\`) {
		volumePath += `\`
	}
	volumePathU16, err := windows.UTF16PtrFromString(volumePath)
	if err != nil {
		t.Fatal(err)
	}
	// Mount point must end in a slash.
	if !strings.HasSuffix(mountPoint, `\`) {
		mountPoint += `\`
	}
	mountPointU16, err := windows.UTF16PtrFromString(mountPoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetVolumeMountPoint(mountPointU16, volumePathU16); err != nil {
		t.Fatalf("failed to mount %s onto %s: %s", volumePath, mountPoint, err)
	}
	t.Cleanup(func() {
		if err := windows.DeleteVolumeMountPoint(mountPointU16); err != nil {
			t.Fatalf("failed to delete mount on %s: %s", mountPoint, err)
		}
	})
}

func TestResolvePath(t *testing.T) {
	// Set up some data to be used by the test cases.
	volumePathC := getVolumeGUIDPath(t, `C:\`)
	dir := t.TempDir()

	makeSymlink(t, `C:\windows`, filepath.Join(dir, "lnk1"))
	makeSymlink(t, `\\localhost\c$\windows`, filepath.Join(dir, "lnk2"))

	volumePathVHD1 := setupVHDVolume(t, filepath.Join(dir, "foo.vhdx"))
	writeFile(t, filepath.Join(volumePathVHD1, "data.txt"), []byte("test content 1"))
	makeSymlink(t, filepath.Join(volumePathVHD1, "data.txt"), filepath.Join(dir, "lnk3"))

	volumePathVHD2 := setupVHDVolume(t, filepath.Join(dir, "bar.vhdx"))
	writeFile(t, filepath.Join(volumePathVHD2, "data.txt"), []byte("test content 2"))
	makeSymlink(t, filepath.Join(volumePathVHD2, "data.txt"), filepath.Join(dir, "lnk4"))
	mountVolume(t, volumePathVHD2, filepath.Join(dir, "mnt"))

	for _, tc := range []struct {
		input       string
		expected    string
		description string
	}{
		{`C:\windows`, volumePathC + `Windows`, "local path"},
		{filepath.Join(dir, "lnk1"), volumePathC + `Windows`, "symlink to local path"},
		{`\\localhost\c$\windows`, `\\localhost\c$\windows`, "UNC path"},
		{filepath.Join(dir, "lnk2"), `\\localhost\c$\windows`, "symlink to UNC path"},
		{filepath.Join(volumePathVHD1, "data.txt"), filepath.Join(volumePathVHD1, "data.txt"), "volume with no mount point"},
		{filepath.Join(dir, "lnk3"), filepath.Join(volumePathVHD1, "data.txt"), "symlink to volume with no mount point"},
		{filepath.Join(dir, "mnt", "data.txt"), filepath.Join(volumePathVHD2, "data.txt"), "volume with mount point"},
		{filepath.Join(dir, "lnk4"), filepath.Join(volumePathVHD2, "data.txt"), "symlink to volume with mount point"},
	} {
		t.Run(tc.description, func(t *testing.T) {
			actual, err := resolvePath(tc.input)
			if err != nil {
				t.Fatalf("resolvePath should return no error, but %v", err)
			}
			if actual != tc.expected {
				t.Fatalf("expected %v but got %v", tc.expected, actual)
			}
			// Make sure EvalSymlinks works with the resolved path, as an extra safety measure.
			p, err := filepath.EvalSymlinks(actual)
			if err != nil {
				t.Fatalf("EvalSymlinks should return no error, but %v", err)
			}
			// As an extra-extra safety, check that resolvePath(x) == EvalSymlinks(resolvePath(x)).
			// EvalSymlinks normalizes UNC path casing, but resolvePath doesn't, so compare with
			// case-insensitivity here.
			if !strings.EqualFold(actual, p) {
				t.Fatalf("EvalSymlinks should resolve to the same path. Expected %v but got %v", actual, p)
			}
		})
	}
}
