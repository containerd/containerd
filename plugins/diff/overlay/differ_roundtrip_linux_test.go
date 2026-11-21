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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/pkg/archive"
)

func assertWalkingFallback(t *testing.T, lower, upper string) {
	t.Helper()
	err := requireSafeUpperDir(context.Background(), lower, upper)
	if !errdefs.IsNotImplemented(err) {
		t.Fatalf("expected walking differ fallback, got %v", err)
	}
}

func assertWalkingRoundTrip(t *testing.T, lower, merged, target string) {
	t.Helper()
	var buf bytes.Buffer
	if err := archive.WriteDiff(context.Background(), &buf, lower, merged); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Apply(context.Background(), target, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
}

// A dangling symlink in the lower tree must be removed with an OCI whiteout,
// not archived as an added overlayfs character device.
func TestDanglingSymlinkWhiteoutUsesWalkingFallback(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("overlay character-device whiteouts require root")
	}
	lower, upper, merged, target := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{lower, target} {
		if err := os.Symlink("missing-target", filepath.Join(root, "gone")); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mknod(filepath.Join(upper, "gone"), unix.S_IFCHR|0600, 0); err != nil {
		t.Skipf("creating overlay whiteout not permitted: %v", err)
	}

	assertWalkingFallback(t, lower, upper)
	assertWalkingRoundTrip(t, lower, merged, target)
	if _, err := os.Lstat(filepath.Join(target, "gone")); !os.IsNotExist(err) {
		t.Fatalf("dangling link still exists after whiteout: %v", err)
	}
}

// A lower symlink must not be followed into an unrelated path when an upper
// entry replaces it (including when replacing a symlink by a directory).
func TestSymlinkReplacementUsesWalkingFallback(t *testing.T) {
	lower, upper, target := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{lower, target} {
		if err := os.Symlink("missing-target", filepath.Join(root, "entry")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(upper, "entry"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upper, "entry", "child"), []byte("contents"), 0644); err != nil {
		t.Fatal(err)
	}

	assertWalkingFallback(t, lower, upper)
	assertWalkingRoundTrip(t, lower, upper, target)
	data, err := os.ReadFile(filepath.Join(target, "entry", "child"))
	if err != nil || string(data) != "contents" {
		t.Fatalf("replaced symlink child = %q, error %v", data, err)
	}
}

// Directory-only chown operations can leave size, mode and mtime unchanged;
// the continuity fast path then skips the metadata change.
func TestDirectoryOwnershipUsesWalkingFallback(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing directory ownership requires root")
	}
	lower, upper, target := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{lower, upper, target} {
		if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	mtime := time.Unix(1700000000, 0)
	for _, root := range []string{lower, upper} {
		if err := os.Chtimes(filepath.Join(root, "dir"), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	owner := os.Getuid() + 1
	if err := os.Chown(filepath.Join(upper, "dir"), owner, os.Getgid()); err != nil {
		t.Skipf("cannot change directory owner: %v", err)
	}

	assertWalkingFallback(t, lower, upper)
	assertWalkingRoundTrip(t, lower, upper, target)
	stat, err := os.Lstat(filepath.Join(target, "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if got := stat.Sys().(*syscall.Stat_t).Uid; int(got) != owner {
		t.Fatalf("applied directory owner = %d, want %d", got, owner)
	}
}

func TestFileReplacedByDirectoryUsesWalkingFallback(t *testing.T) {
	lower, upper, target := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{lower, target} {
		if err := os.WriteFile(filepath.Join(root, "entry"), []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(upper, "entry"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upper, "entry", "child"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	assertWalkingFallback(t, lower, upper)
	assertWalkingRoundTrip(t, lower, upper, target)
	data, err := os.ReadFile(filepath.Join(target, "entry", "child"))
	if err != nil || string(data) != "new" {
		t.Fatalf("replaced file child = %q, error %v", data, err)
	}
}

func TestCancelledUpperdirInspection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := requireSafeUpperDir(ctx, t.TempDir(), t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled upperdir inspection, got %v", err)
	}
	called := false
	handler := withCancellation(ctx, func(intKind fs.ChangeKind, path string, info os.FileInfo, err error) error {
		called = true
		return nil
	})
	if err := handler(fs.ChangeKindAdd, "/entry", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled diff callback, got %v", err)
	}
	if called {
		t.Fatal("canceled callback should not write a change")
	}

	var contents bytes.Buffer
	writer := contextWriter{ctx: ctx, writer: &contents}
	if _, err := writer.Write([]byte("must not be written")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled archive writer, got %v", err)
	}
	if contents.Len() != 0 {
		t.Fatalf("archive writer wrote %d bytes after cancellation", contents.Len())
	}
}
