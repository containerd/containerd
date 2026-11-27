//go:build !windows && !darwin

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

package blockfile

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

func TestViewExt4JournalRecovery(t *testing.T) {
	testutil.RequiresRoot(t)
	debugfs, err := exec.LookPath("debugfs")
	if err != nil {
		t.Skip("debugfs is required to prepare an ext4 image that needs journal recovery")
	}
	ctx := t.Context()
	opts, err := setupSnapshotter(t)
	require.NoError(t, err)
	opts = append(opts, withViewHookHelper(nil))
	root := t.TempDir()
	s, err := NewSnapshotter(root, opts...)
	require.NoError(t, err)
	t.Cleanup(func() {
		if s != nil {
			require.NoError(t, s.Close())
		}
	})

	active, err := s.Prepare(ctx, "active", "")
	require.NoError(t, err)
	const payload = "blockfile contents\n"
	require.NoError(t, mount.WithTempMount(ctx, active, func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "data"), []byte(payload), 0600)
	}))
	backing := active[0].Source
	// Mark the unmounted image as needing replay without racing a live ext4 writer.
	out, err := exec.CommandContext(ctx, debugfs, "-w", "-R", "feature needs_recovery", backing).CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.NoError(t, s.Commit(ctx, "committed", "active"))

	before, err := os.ReadFile(backing)
	require.NoError(t, err)
	view, err := s.View(ctx, "view", "committed")
	require.NoError(t, err)
	require.NoError(t, mount.WithTempMount(ctx, view, func(dir string) error {
		data, err := os.ReadFile(filepath.Join(dir, "data"))
		require.NoError(t, err)
		require.Equal(t, payload, string(data))
		require.ErrorIs(t, os.WriteFile(filepath.Join(dir, "write"), nil, 0600), unix.EROFS)
		return nil
	}))
	require.NotEqual(t, backing, view[0].Source)
	usage, err := s.Usage(ctx, "view")
	require.NoError(t, err)
	require.Equal(t, int64(len(before)), usage.Size)
	require.Equal(t, int64(1), usage.Inodes)
	require.NoError(t, s.Close())
	s, err = NewSnapshotter(root, opts...)
	require.NoError(t, err)
	mounts, err := s.Mounts(ctx, "view")
	require.NoError(t, err)
	require.Equal(t, view, mounts)
	require.NoError(t, s.Remove(ctx, "view"))
	require.NoFileExists(t, view[0].Source)
	after, err := os.ReadFile(backing)
	require.NoError(t, err)
	require.True(t, bytes.Equal(before, after), "journal recovery modified the committed image")
}

func TestViewAfterLazyExt4Unmount(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := t.Context()
	opts, err := setupSnapshotter(t)
	require.NoError(t, err)
	s, err := NewSnapshotter(t.TempDir(), append(opts, withViewHookHelper(nil))...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	active, err := s.Prepare(ctx, "active", "")
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, mount.All(active, dir))
	t.Cleanup(func() { require.NoError(t, mount.UnmountAll(dir, 0)) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data"), []byte("test"), 0600))
	ref, err := os.Open(dir)
	require.NoError(t, err)
	defer ref.Close()
	require.NoError(t, mount.Unmount(dir, unix.MNT_DETACH))
	require.NoError(t, s.Commit(ctx, "committed", "active"))

	// The detached mount is still live until its last open reference closes.
	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = s.View(waitCtx, "view", "committed")
	require.ErrorIs(t, err, errdefs.ErrUnavailable)
	_, err = s.Stat(ctx, "view")
	require.ErrorIs(t, err, errdefs.ErrNotFound)

	require.NoError(t, ref.Close())
	view, err := s.View(ctx, "view", "committed")
	require.NoError(t, err)
	require.Equal(t, active[0].Source, view[0].Source)
	require.NoError(t, mount.WithTempMount(ctx, view, func(dir string) error {
		data, err := os.ReadFile(filepath.Join(dir, "data"))
		require.NoError(t, err)
		require.Equal(t, "test", string(data))
		return nil
	}))
}

func setupSnapshotter(t *testing.T) ([]Opt, error) {
	mkfs, err := exec.LookPath("mkfs.ext4")
	if err != nil {
		t.Skipf("Could not find mkfs.ext4: %v", err)
	}

	loopbackSize := int64(8 << 20) // 8 MB
	if os.Getpagesize() > 4096 {
		loopbackSize = int64(16 << 20) // 16 MB
	}

	scratch := filepath.Join(t.TempDir(), "scratch")
	scratchDevFile, err := os.OpenFile(scratch, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", scratch, err)
	}

	if err := scratchDevFile.Truncate(loopbackSize); err != nil {
		scratchDevFile.Close()
		return nil, fmt.Errorf("failed to resize %s file: %w", scratch, err)
	}

	if err := scratchDevFile.Sync(); err != nil {
		scratchDevFile.Close()
		return nil, fmt.Errorf("failed to sync %s file: %w", scratch, err)
	}
	scratchDevFile.Close()

	if out, err := exec.Command(mkfs, scratch).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to make ext4 filesystem (out: %q): %w", out, err)
	}

	defaultOpts := []string{"loop", "direct-io", "sync"}

	if err := testMount(t, scratch, defaultOpts); err != nil {
		return nil, err
	}

	return []Opt{
		WithScratchFile(scratch),
		WithFSType("ext4"),
		WithMountOptions(defaultOpts),
		WithRecreateScratch(false), // reduce IO presure in CI
		withViewHookHelper(testViewHook),
	}, nil
}

func testMount(t *testing.T, scratchFile string, opts []string) error {
	root := t.TempDir()

	m := []mount.Mount{
		{
			Type:    "ext4",
			Source:  scratchFile,
			Options: opts,
		},
	}

	if err := mount.All(m, root); err != nil {
		return fmt.Errorf("failed to mount device %s: %w", scratchFile, err)
	}

	if err := os.Remove(filepath.Join(root, "lost+found")); err != nil {
		return err
	}
	return mount.UnmountAll(root, 0)
}

func testViewHook(backingFile string, fsType string, defaultOpts []string) error {
	root, err := os.MkdirTemp("", "blockfile-testViewHook-XXXX")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	// FIXME(fuweid): Mount with rw to force fs to handle recover
	mountOpts := []mount.Mount{
		{
			Type:    fsType,
			Source:  backingFile,
			Options: defaultOpts,
		},
	}

	if err := mount.All(mountOpts, root); err != nil {
		return fmt.Errorf("failed to mount device %s: %w", backingFile, err)
	}

	if err := mount.UnmountAll(root, 0); err != nil {
		return fmt.Errorf("failed to unmount device %s: %w", backingFile, err)
	}
	return nil
}
