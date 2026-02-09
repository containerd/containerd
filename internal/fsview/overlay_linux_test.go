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
	"errors"
	"io/fs"
	"syscall"
	"testing"
	"testing/fstest"
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

	ofs, err := NewOverlayFS([]fs.FS{upper, lower})
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
