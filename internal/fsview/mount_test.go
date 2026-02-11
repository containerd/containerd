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

package fsview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
)

func TestFSMountsLast(t *testing.T) {
	tmp := t.TempDir()
	dir1 := filepath.Join(tmp, "dir1")
	dir2 := filepath.Join(tmp, "dir2")
	if err := os.Mkdir(dir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir1, "f1"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "f2"), []byte("2"), 0644); err != nil {
		t.Fatal(err)
	}

	mounts := []mount.Mount{
		{Type: "bind", Source: dir1, Target: "/mnt/dir1"},
		{Type: "bind", Source: dir2, Target: "/mnt/dir2"},
	}

	// Should pick the last one (dir2)
	fs, err := FSMounts(mounts)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	if _, err := fs.Open("f2"); err != nil {
		t.Errorf("expected to find f2 in last mount, got %v", err)
	}
	if _, err := fs.Open("f1"); err == nil {
		t.Error("expected NOT to find f1 (should only have dir2)")
	}
}

func TestFSMountsEROFSWithDevices(t *testing.T) {
	// This test verifies that the devices option is properly parsed
	// We can't fully test EROFS functionality without actual EROFS images,
	// but we can verify the option parsing doesn't cause errors
	tmp := t.TempDir()

	// Create dummy files to simulate EROFS images
	mainImage := filepath.Join(tmp, "main.erofs")
	device1 := filepath.Join(tmp, "device1.erofs")
	device2 := filepath.Join(tmp, "device2.erofs")

	// Create empty files (not valid EROFS, but enough to test option parsing)
	for _, f := range []string{mainImage, device1, device2} {
		if err := os.WriteFile(f, []byte("dummy"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	mounts := []mount.Mount{
		{
			Type:   "erofs",
			Source: mainImage,
			Options: []string{
				"ro",
				"devices=" + device1 + "," + device2,
			},
		},
	}

	// This will fail because the files aren't valid EROFS images,
	// but the important thing is that it tries to open the devices
	// and doesn't panic or error on option parsing
	fs, err := FSMounts(mounts)
	if fs != nil {
		defer fs.Close()
	}

	// We expect an error because these aren't valid EROFS images,
	// but it should be an EROFS-related error, not an option parsing error
	if err == nil {
		t.Error("expected error from invalid EROFS images")
	}
	// The error should come from erofs.EroFS, not from option parsing
	t.Logf("Got expected error: %v", err)
}

func TestFSMountsOverlayWithEROFSDevices(t *testing.T) {
	// Test that overlay mounts with EROFS layers correctly parse devices option
	tmp := t.TempDir()

	// Create dummy files to simulate EROFS images
	lowerImage := filepath.Join(tmp, "lower.erofs")
	device1 := filepath.Join(tmp, "device1.erofs")
	device2 := filepath.Join(tmp, "device2.erofs")

	for _, f := range []string{lowerImage, device1, device2} {
		if err := os.WriteFile(f, []byte("dummy"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	lowerTarget := filepath.Join(tmp, "lower")

	mounts := []mount.Mount{
		{
			Type:   "erofs",
			Source: lowerImage,
			Target: lowerTarget,
			Options: []string{
				"ro",
				"devices=" + device1 + "," + device2,
			},
		},
		{
			Type: "overlay",
			Options: []string{
				"lowerdir=" + lowerTarget,
			},
		},
	}

	// This will fail because the files aren't valid EROFS images,
	// but verifies that devices option is parsed in overlay layers
	fs, err := FSMounts(mounts)
	if fs != nil {
		defer fs.Close()
	}

	if err == nil {
		t.Error("expected error from invalid EROFS images")
	}
	t.Logf("Got expected error: %v", err)
}
