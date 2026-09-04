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

package archive

import (
	"os"
	"path/filepath"
	"testing"
)

// modeOf returns the permission bits (including setuid/setgid/sticky) of path,
// without following symlinks.
func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return fi.Mode()
}

// TestLchmodDoesNotFollowSymlink asserts that lchmod never changes the mode of
// a symlink's target. This is the metadata-op half of the TOCTOU race: an
// attacker who swaps a freshly created file for a symlink must not be able to
// steer the chmod onto the link target.
func TestLchmodDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()

	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := lchmod(link, 0o777); err != nil {
		t.Fatalf("lchmod on symlink returned error: %v", err)
	}

	if got := modeOf(t, target).Perm(); got != 0o600 {
		t.Fatalf("symlink target mode changed to %o, want 0600 (chmod followed the symlink)", got)
	}
}

// TestLchmodRegularFilePreservesSpecialBits asserts lchmod still applies the
// mode to a real file and that setuid survives the os.FileMode -> syscall mode
// conversion (a plain uint32 cast would have dropped it).
func TestLchmodRegularFilePreservesSpecialBits(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "reg")
	if err := os.WriteFile(reg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// tar's FileInfo().Mode() uses os.FileMode's ModeSetuid bit for 04755.
	want := os.FileMode(0o755) | os.ModeSetuid
	if err := lchmod(reg, want); err != nil {
		t.Fatalf("lchmod on regular file: %v", err)
	}

	got := modeOf(t, reg)
	if got.Perm() != 0o755 || got&os.ModeSetuid == 0 {
		t.Fatalf("mode = %v, want 0755 with setuid set", got)
	}
}

// TestFchmodDirRefusesSymlink asserts fchmodDir will not chmod through a symlink
// standing where a directory is expected, and leaves the link target untouched.
func TestFchmodDirRefusesSymlink(t *testing.T) {
	dir := t.TempDir()

	targetDir := filepath.Join(dir, "targetdir")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "dlink")
	if err := os.Symlink(targetDir, link); err != nil {
		t.Fatal(err)
	}

	if err := fchmodDir(link, 0o777); err == nil {
		t.Fatal("fchmodDir followed a symlink; expected an error")
	}
	if got := modeOf(t, targetDir).Perm(); got != 0o700 {
		t.Fatalf("target dir mode changed to %o, want 0700 (chmod followed the symlink)", got)
	}
}

// TestFchmodDirChmodsRealDir asserts the happy path still works.
func TestFchmodDirChmodsRealDir(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "d")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fchmodDir(d, 0o750); err != nil {
		t.Fatalf("fchmodDir: %v", err)
	}
	if got := modeOf(t, d).Perm(); got != 0o750 {
		t.Fatalf("dir mode = %o, want 0750", got)
	}
}
