//go:build linux && !no_btrfs && cgo

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

package btrfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/testsuite"
	"github.com/containerd/continuity/testutil/loopback"
	exec "golang.org/x/sys/execabs"
	"golang.org/x/sys/unix"
)

func boltSnapshotter(t *testing.T) func(context.Context, string) (snapshots.Snapshotter, func() error, error) {
	mkbtrfs, err := exec.LookPath("mkfs.btrfs")
	if err != nil {
		t.Skipf("could not find mkfs.btrfs: %v", err)
	}

	procModules, err := os.ReadFile("/proc/modules")
	if err == nil && !bytes.Contains(procModules, []byte("btrfs")) {
		t.Skip("check for btrfs kernel module failed, skipping test")
	}

	return func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {

		loopbackSize := int64(128 << 20) // 128 MB
		// mkfs.btrfs creates a fs which has a blocksize equal to the system default pagesize. If that pagesize
		// is > 4KB, mounting the fs will fail unless we increase the size of the file used by mkfs.btrfs
		if os.Getpagesize() > 4096 {
			loopbackSize = int64(650 << 20) // 650 MB
		}
		loop, err := loopback.New(loopbackSize)

		if err != nil {
			return nil, nil, err
		}

		if out, err := exec.Command(mkbtrfs, loop.Device).CombinedOutput(); err != nil {
			loop.Close()
			return nil, nil, fmt.Errorf("failed to make btrfs filesystem (out: %q): %w", out, err)
		}
		// sync after a mkfs on the loopback before trying to mount the device
		unix.Sync()

		var snapshotter snapshots.Snapshotter
		for i := 0; i < 5; i++ {
			if out, err := exec.Command("mount", loop.Device, root).CombinedOutput(); err != nil {
				loop.Close()
				return nil, nil, fmt.Errorf("failed to mount device %s (out: %q): %w", loop.Device, out, err)
			}

			if i > 0 {
				time.Sleep(10 * time.Duration(i) * time.Millisecond)
			}

			snapshotter, err = NewSnapshotter(root)
			if err == nil {
				break
			} else if !errors.Is(err, plugin.ErrSkipPlugin) {
				return nil, nil, err
			}

			t.Logf("Attempt %d to create btrfs snapshotter failed: %#v", i+1, err)

			// unmount and try again
			unix.Unmount(root, 0)
		}
		if snapshotter == nil {
			return nil, nil, fmt.Errorf("failed to successfully create snapshotter after 5 attempts: %w", err)
		}

		return snapshotter, func() error {
			if err := snapshotter.Close(); err != nil {
				return err
			}
			err := mount.UnmountAll(root, 0)
			if cerr := loop.Close(); cerr != nil {
				err = fmt.Errorf("device cleanup failed: %w", cerr)
			}
			return err
		}, nil
	}
}

func TestBtrfs(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Btrfs", boltSnapshotter(t))
}

func TestBtrfsMounts(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := context.Background()

	root := t.TempDir()

	b, c, err := boltSnapshotter(t)(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer c()

	target := filepath.Join(root, "test")
	mounts, err := b.Prepare(ctx, target, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(mounts)

	for _, mount := range mounts {
		if mount.Type != "btrfs" {
			t.Fatalf("wrong mount type: %v != btrfs", mount.Type)
		}

		// assumes the first, maybe incorrect in the future
		if !strings.HasPrefix(mount.Options[0], "subvolid=") {
			t.Fatalf("no subvolid option in %v", mount.Options)
		}
	}

	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := mount.All(mounts, target); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, target)

	// write in some data
	if err := os.WriteFile(filepath.Join(target, "foo"), []byte("content"), 0777); err != nil {
		t.Fatal(err)
	}

	// TODO(stevvooe): We don't really make this with the driver, but that
	// might prove annoying in practice.
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := b.Commit(ctx, filepath.Join(root, "snapshots/committed"), filepath.Join(root, "test")); err != nil {
		t.Fatal(err)
	}

	target = filepath.Join(root, "test2")
	mounts, err = b.Prepare(ctx, target, filepath.Join(root, "snapshots/committed"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	if err := mount.All(mounts, target); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, target)

	bs, err := os.ReadFile(filepath.Join(target, "foo"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bs) != "content" {
		t.Fatalf("wrong content in foo want: content, got: %s", bs)
	}

	if err := os.WriteFile(filepath.Join(target, "bar"), []byte("content"), 0777); err != nil {
		t.Fatal(err)
	}

	if err := b.Commit(ctx, filepath.Join(root, "snapshots/committed2"), target); err != nil {
		t.Fatal(err)
	}
}
