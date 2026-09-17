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
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay/overlayutils"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/continuity/fs/fstest"
	"github.com/containerd/log/logtest"
	"golang.org/x/sys/unix"
)

func TestOverlayApply(t *testing.T) {
	testutil.RequiresRoot(t)

	base := t.TempDir()

	if err := overlayutils.Supported(base); err != nil {
		t.Skipf("skipping because overlay is not supported %v", err)
	}
	fstest.FSSuite(t, overlayDiffApplier{
		tmp:  base,
		diff: WriteDiff,
		t:    t,
	})
}

func TestOverlayApplyNoParents(t *testing.T) {
	testutil.RequiresRoot(t)

	base := t.TempDir()

	if err := overlayutils.Supported(base); err != nil {
		t.Skipf("skipping because overlay is not supported %v", err)
	}
	fstest.FSSuite(t, overlayDiffApplier{
		tmp: base,
		diff: func(ctx context.Context, w io.Writer, a, b string, _ ...WriteDiffOpt) error {
			cw := NewChangeWriter(w, b)
			cw.addedDirs = nil
			err := fs.Changes(ctx, a, b, cw.HandleChange)
			if err != nil {
				return fmt.Errorf("failed to create diff tar stream: %w", err)
			}
			return cw.Close()
		},
		t: t,
	})
}

type overlayDiffApplier struct {
	tmp  string
	diff func(context.Context, io.Writer, string, string, ...WriteDiffOpt) error
	t    *testing.T
}

type overlayContext struct {
	merged  string
	lowers  []string
	mounted bool
}

type contextKey struct{}

func (d overlayDiffApplier) TestContext(ctx context.Context) (context.Context, func(), error) {
	merged, err := os.MkdirTemp(d.tmp, "merged")
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to make merged dir: %w", err)
	}

	oc := &overlayContext{
		merged: merged,
	}

	ctx = logtest.WithT(ctx, d.t)

	return context.WithValue(ctx, contextKey{}, oc), func() {
		if oc.mounted {
			mount.Unmount(oc.merged, 0)
		}
	}, nil
}

func (d overlayDiffApplier) Apply(ctx context.Context, a fstest.Applier) (string, func(), error) {
	oc := ctx.Value(contextKey{}).(*overlayContext)

	applyCopy, err := os.MkdirTemp(d.tmp, "apply-copy-")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(applyCopy)

	base := oc.merged
	if len(oc.lowers) == 1 {
		base = oc.lowers[0]
	}

	if err = fs.CopyDir(applyCopy, base); err != nil {
		return "", nil, fmt.Errorf("failed to copy base: %w", err)
	}

	if err := a.Apply(applyCopy); err != nil {
		return "", nil, fmt.Errorf("failed to apply changes to copy of base: %w", err)
	}

	buf := bytes.NewBuffer(nil)

	if err := d.diff(ctx, buf, base, applyCopy); err != nil {
		return "", nil, fmt.Errorf("failed to create diff: %w", err)
	}

	if oc.mounted {
		if err := mount.Unmount(oc.merged, 0); err != nil {
			return "", nil, fmt.Errorf("failed to unmount: %w", err)
		}
		oc.mounted = false
	}

	next, err := os.MkdirTemp(d.tmp, "lower-")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	if _, err = Apply(ctx, next, buf, WithConvertWhiteout(OverlayConvertWhiteout), WithParents(oc.lowers)); err != nil {
		return "", nil, fmt.Errorf("failed to apply tar stream: %w", err)
	}

	oc.lowers = append([]string{next}, oc.lowers...)

	if len(oc.lowers) == 1 {
		return oc.lowers[0], nil, nil
	}

	m := mount.Mount{
		Type:   "overlay",
		Source: "overlay",
		Options: []string{
			fmt.Sprintf("lowerdir=%s", strings.Join(oc.lowers, ":")),
		},
	}

	if err := m.Mount(oc.merged); err != nil {
		return "", nil, fmt.Errorf("failed to mount: %v: %w", m, err)
	}
	oc.mounted = true

	return oc.merged, nil, nil
}

func TestLchmodRestrictiveParent(t *testing.T) {
	// The parent handle is opened with O_PATH, so lchmod only needs search
	// permission on the directory, as the lstat and chmod by name it replaced
	// did. Only an unprivileged run can tell the difference; root gets
	// through either way.
	dir := filepath.Join(t.TempDir(), "dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "file")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	// Restore read permission so the TempDir cleanup can list it.
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if err := lchmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %o, want 0640", got)
	}
}

func TestLchmodFallback(t *testing.T) {
	// Kernels with fchmodat2 only reach the fallback for symlinks, so call it
	// directly to cover both the symlink and the regular file case.
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	dirfd, err := unix.Open(dir, openParentFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirfd)

	if err := lchmodFallback(dirfd, "link", 0o777); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("symlink target mode changed to %o, want 0600", got)
	}

	want := os.FileMode(0o640) | os.ModeSetuid
	if err := lchmodFallback(dirfd, "target", want); err != nil {
		t.Fatal(err)
	}
	if fi, err = os.Lstat(target); err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky); got != want {
		t.Fatalf("mode = %o, want %o", got, want)
	}
}
