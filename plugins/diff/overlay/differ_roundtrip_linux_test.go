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
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/containerd/v2/pkg/archive"
	"golang.org/x/sys/unix"
)

// The fast path must preserve a whiteout when deleting a dangling symlink.
// The overlay whiteout representation needs mknod privileges on the test FS.
func TestDiffDirChangesDanglingSymlinkDeletion(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("creating overlay whiteout devices requires root")
	}
	base, upper, target := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Symlink("missing-target", filepath.Join(base, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", filepath.Join(target, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mknod(filepath.Join(upper, "gone"), unix.S_IFCHR|0600, 0); err != nil {
		t.Skipf("creating overlay whiteout not permitted: %v", err)
	}
	var buf bytes.Buffer
	cw := archive.NewChangeWriter(&buf, upper)
	if err := fs.DiffDirChanges(context.Background(), base, upper, fs.DiffSourceOverlayFS, cw.HandleChange); err != nil {
		t.Fatal(err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != ".wh.gone" {
		t.Fatalf("expected .wh.gone, got %q", hdr.Name)
	}
	if _, err := archive.Apply(context.Background(), target, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(target, "gone")); !os.IsNotExist(err) {
		t.Fatalf("deleted dangling symlink still exists or stat failed unexpectedly: %v", err)
	}
}

// A directory chown with unchanged mode, size, and mtime must be archived
// and survive application of the resulting layer.
func TestDiffDirChangesDirectoryOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing directory ownership requires root")
	}
	base, upper, target := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{base, upper, target} {
		if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	mtime := time.Unix(1700000000, 0)
	for _, root := range []string{base, upper} {
		if err := os.Chtimes(filepath.Join(root, "dir"), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	owner := os.Getuid() + 1
	if err := os.Chown(filepath.Join(upper, "dir"), owner, os.Getgid()); err != nil {
		t.Skipf("cannot change directory owner: %v", err)
	}
	var buf bytes.Buffer
	cw := archive.NewChangeWriter(&buf, upper)
	if err := fs.DiffDirChanges(context.Background(), base, upper, fs.DiffSourceOverlayFS, cw.HandleChange); err != nil {
		t.Fatal(err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}
	hdr, err := tar.NewReader(bytes.NewReader(buf.Bytes())).Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "dir/" || hdr.Uid != owner {
		t.Fatalf("expected dir/ with uid %d, got %q with uid %d", owner, hdr.Name, hdr.Uid)
	}
	if _, err := archive.Apply(context.Background(), target, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(target, "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Sys().(*unix.Stat_t).Uid; int(got) != owner {
		t.Fatalf("applied directory owner = %d, want %d", got, owner)
	}
}
