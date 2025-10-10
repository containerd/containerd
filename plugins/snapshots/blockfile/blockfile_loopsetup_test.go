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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
)

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
