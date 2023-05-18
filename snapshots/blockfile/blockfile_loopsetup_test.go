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

	"github.com/containerd/containerd/mount"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/continuity/testutil/loopback"
	"golang.org/x/sys/unix"
)

func setupSnapshotter(t *testing.T) ([]Opt, error) {
	mkfs, err := exec.LookPath("mkfs.ext4")
	if err != nil {
		t.Skipf("Could not find mkfs.ext4: %v", err)
	}

	loopbackSize := int64(128 << 20) // 128 MB
	if os.Getpagesize() > 4096 {
		loopbackSize = int64(650 << 20) // 650 MB
	}

	loop, err := loopback.New(loopbackSize)
	if err != nil {
		return nil, err
	}
	defer loop.Close()

	if out, err := exec.Command(mkfs, loop.Device).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to make ext4 filesystem (out: %q): %w", out, err)
	}
	// sync after a mkfs on the loopback before trying to mount the device
	unix.Sync()

	if err := testMount(t, loop.Device); err != nil {
		return nil, err
	}

	scratch := filepath.Join(t.TempDir(), "scratch")
	err = fs.CopyFile(scratch, loop.File)
	if err != nil {
		return nil, err
	}

	return []Opt{
		WithScratchFile(scratch),
	}, nil
}

func testMount(t *testing.T, device string) error {
	root, err := os.MkdirTemp(t.TempDir(), "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	if out, err := exec.Command("mount", device, root).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to mount device %s (out: %q): %w", device, out, err)
	}
	if err := os.Remove(filepath.Join(root, "lost+found")); err != nil {
		return err
	}
	return mount.UnmountAll(root, unix.MNT_DETACH)
}
