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

package fsview_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/fsview"

	"golang.org/x/sys/unix"
)

func TestOverlayFS(t *testing.T) {
	lower := fstest.MapFS{
		"file1":       &fstest.MapFile{Data: []byte("lower-file1")},
		"dir1/file2":  &fstest.MapFile{Data: []byte("lower-file2")},
		"dir1/hidden": &fstest.MapFile{Data: []byte("lower-hidden")},
		"dir2/file3":  &fstest.MapFile{Data: []byte("lower-file3")},
	}

	// upper has a whiteout for "dir1/hidden"
	// To simulate whiteout, we need ModeCharDevice and Rdev=0.
	// fstest.MapFile Mode is fs.FileMode.
	upper := fstest.MapFS{
		"file1":       &fstest.MapFile{Data: []byte("upper-file1")},
		"dir1/hidden": &fstest.MapFile{Mode: fs.ModeCharDevice, Sys: &syscall.Stat_t{Rdev: 0}},
		"dir3/file4":  &fstest.MapFile{Data: []byte("upper-file4")},
	}

	ofs, err := fsview.NewOverlayFS([]fs.FS{upper, lower})
	if err != nil {
		t.Fatal(err)
	}

	// Test file1 (upper overrides lower)
	data, err := fs.ReadFile(ofs, "file1")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "upper-file1" {
		t.Errorf("expected upper-file1, got %s", data)
	}

	// Test dir1/file2 (from lower, merged dir)
	data, err = fs.ReadFile(ofs, "dir1/file2")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "lower-file2" {
		t.Errorf("expected lower-file2, got %s", data)
	}

	// Test dir1/hidden (whiteout in upper should hide lower)
	// fs.ReadFile fails on device files anyway, but here it shouldn't even find it (NotExist).
	// Wait, isWhiteout in Open returns NotExist.
	_, err = fs.ReadFile(ofs, "dir1/hidden")
	if err == nil {
		t.Fatal("expected error for whiteout, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected NotExist, got %v", err)
	}

	// Test dir2/file3 (only in lower)
	data, err = fs.ReadFile(ofs, "dir2/file3")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "lower-file3" {
		t.Errorf("expected lower-file3, got %s", data)
	}

	// Test ReadDir merging in dir1
	entries, err := fs.ReadDir(ofs, "dir1")
	if err != nil {
		t.Fatal(err)
	}
	// Should contain: file2. hidden is whiteout so it should NOT appear.
	foundFile2 := false
	foundHidden := false
	for _, e := range entries {
		if e.Name() == "file2" {
			foundFile2 = true
		}
		if e.Name() == "hidden" {
			foundHidden = true
		}
	}
	if !foundFile2 {
		t.Error("dir1/file2 not found in ReadDir")
	}
	if foundHidden {
		t.Error("dir1/hidden should not be found in ReadDir")
	}

	// Test dir3/file4 (only in upper)
	data, err = fs.ReadFile(ofs, "dir3/file4")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "upper-file4" {
		t.Errorf("expected upper-file4, got %s", data)
	}
}

// TestOverlayFSDirReplacedByFile tests that when an upper layer replaces a
// directory with a regular file, child paths return ErrNotExist rather than
// a confusing "not a directory" error.
// TestOverlayFSCrossLayerSymlink tests that a symlink in a lower layer
// correctly resolves to a file in an upper layer when the symlink target
// has been replaced by the upper layer.
func TestOverlayFSCrossLayerSymlink(t *testing.T) {
	base := t.TempDir()
	upper := filepath.Join(base, "upper")
	lower := filepath.Join(base, "lower")

	// Lower layer: has a symlink etc/config -> ../opt/config
	// and the original target file
	if err := os.MkdirAll(filepath.Join(lower, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(lower, "opt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../opt/config", filepath.Join(lower, "etc", "config")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lower, "opt", "config"), []byte("old-config"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Upper layer: replaces opt/config with new content
	if err := os.MkdirAll(filepath.Join(upper, "opt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upper, "opt", "config"), []byte("new-config"), 0o644); err != nil {
		t.Fatal(err)
	}

	lowerdir := strings.Join([]string{upper, lower}, ":")
	v, err := fsview.FSMounts([]mount.Mount{
		{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				"lowerdir=" + lowerdir,
			},
		},
	})
	if err != nil {
		t.Fatalf("FSMounts failed: %v", err)
	}
	defer v.Close()

	// Opening etc/config should follow the symlink through the overlay
	// and find opt/config from the upper layer (new-config), not the lower.
	data, err := fs.ReadFile(v, "etc/config")
	if err != nil {
		t.Fatalf("ReadFile etc/config: %v", err)
	}
	if string(data) != "new-config" {
		t.Errorf("expected new-config, got %s", data)
	}

	// Direct open of opt/config should also return upper layer content
	data, err = fs.ReadFile(v, "opt/config")
	if err != nil {
		t.Fatalf("ReadFile opt/config: %v", err)
	}
	if string(data) != "new-config" {
		t.Errorf("expected new-config, got %s", data)
	}
}

// TestOverlayFSAbsoluteSymlinkCrossLayer tests absolute symlinks that
// resolve across layers.
func TestOverlayFSAbsoluteSymlinkCrossLayer(t *testing.T) {
	base := t.TempDir()
	upper := filepath.Join(base, "upper")
	lower := filepath.Join(base, "lower")

	// Lower layer: has /etc/group -> /nix/store/abcd/group
	// and the original target file
	if err := os.MkdirAll(filepath.Join(lower, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(lower, "nix", "store", "abcd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nix/store/abcd/group", filepath.Join(lower, "etc", "group")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lower, "nix", "store", "abcd", "group"), []byte("old-group"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Upper layer: replaces nix/store/abcd/group with new content
	if err := os.MkdirAll(filepath.Join(upper, "nix", "store", "abcd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upper, "nix", "store", "abcd", "group"), []byte("new-group"), 0o644); err != nil {
		t.Fatal(err)
	}

	lowerdir := strings.Join([]string{upper, lower}, ":")
	v, err := fsview.FSMounts([]mount.Mount{
		{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				"lowerdir=" + lowerdir,
			},
		},
	})
	if err != nil {
		t.Fatalf("FSMounts failed: %v", err)
	}
	defer v.Close()

	// Opening etc/group should follow the absolute symlink through the overlay
	// and find nix/store/abcd/group from the upper layer.
	data, err := fs.ReadFile(v, "etc/group")
	if err != nil {
		t.Fatalf("ReadFile etc/group: %v", err)
	}
	if string(data) != "new-group" {
		t.Errorf("expected new-group, got %s", data)
	}
}

// TestOverlayFSSymlinkChain tests chained symlinks across layers.
func TestOverlayFSSymlinkChain(t *testing.T) {
	base := t.TempDir()
	upper := filepath.Join(base, "upper")
	lower := filepath.Join(base, "lower")

	// Lower layer: a -> b (symlink), b -> c (symlink), c is a file
	if err := os.MkdirAll(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(upper, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b", filepath.Join(lower, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("c", filepath.Join(lower, "b")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lower, "c"), []byte("lower-c"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Upper layer: replaces c
	if err := os.WriteFile(filepath.Join(upper, "c"), []byte("upper-c"), 0o644); err != nil {
		t.Fatal(err)
	}

	lowerdir := strings.Join([]string{upper, lower}, ":")
	v, err := fsview.FSMounts([]mount.Mount{
		{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				"lowerdir=" + lowerdir,
			},
		},
	})
	if err != nil {
		t.Fatalf("FSMounts failed: %v", err)
	}
	defer v.Close()

	// Opening a should follow a -> b -> c through the overlay,
	// finding c from the upper layer.
	data, err := fs.ReadFile(v, "a")
	if err != nil {
		t.Fatalf("ReadFile a: %v", err)
	}
	if string(data) != "upper-c" {
		t.Errorf("expected upper-c, got %s", data)
	}
}

func TestOverlayFSDirReplacedByFile(t *testing.T) {
	base := t.TempDir()
	layer1 := filepath.Join(base, "layer1")
	layer2 := filepath.Join(base, "layer2")
	layer3 := filepath.Join(base, "layer3")

	// Layer 1: mkdir -p /dir1/dir2
	if err := os.MkdirAll(filepath.Join(layer1, "dir1", "dir2"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Layer 2: touch /dir1/dir2/foo
	if err := os.MkdirAll(filepath.Join(layer2, "dir1", "dir2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layer2, "dir1", "dir2", "foo"), []byte("foo"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Layer 3: rm -r /dir1 && mkdir -p /dir1 && touch /dir1/dir2
	// (dir1 is opaque, dir2 is now a file instead of a directory)
	if err := os.MkdirAll(filepath.Join(layer3, "dir1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(filepath.Join(layer3, "dir1"), "user.overlay.opaque", []byte{'y'}, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layer3, "dir1", "dir2"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	lowerdir := strings.Join([]string{layer3, layer2, layer1}, ":")
	v, err := fsview.FSMounts([]mount.Mount{
		{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				"lowerdir=" + lowerdir,
			},
		},
	})
	if err != nil {
		t.Fatalf("FSMounts failed: %v", err)
	}
	defer v.Close()

	// dir1/dir2 should be a regular file (from layer3)
	fi, err := fs.Stat(v, "dir1/dir2")
	if err != nil {
		t.Fatalf("stat dir1/dir2: %v", err)
	}
	if fi.IsDir() {
		t.Fatal("expected dir1/dir2 to be a file, got directory")
	}

	// dir1/dir2/foo should not exist — and the error should be ErrNotExist,
	// not a confusing "not a directory" error.
	_, err = v.Open("dir1/dir2/foo")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected dir1/dir2/foo to be ErrNotExist, got %v", err)
	}
}
