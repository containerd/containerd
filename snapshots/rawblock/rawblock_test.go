// +build linux,!no_rawblock

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

package rawblock

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/testsuite"
	"github.com/containerd/continuity/testutil/loopback"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

const mkfsBin = "mkfs.btrfs"

func boltSnapshotter(t *testing.T) func(context.Context, string) (snapshots.Snapshotter, func() error, error) {
	mkfs, err := exec.LookPath(mkfsBin)
	if err != nil {
		t.Skipf("could not find %s: %v", mkfsBin, err)
	}

	return func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {

		loopbackSize := int64(512 << 20) // 512 MB
		loop, err := loopback.New(loopbackSize)
		if err != nil {
			return nil, nil, err
		}

		if out, err := exec.Command(mkfs, loop.Device).CombinedOutput(); err != nil {
			loop.Close()
			return nil, nil, errors.Wrapf(err, "failed to make filesystem (out: %q)", out)
		}
		// sync after a mkfs on the loopback before trying to mount the device
		unix.Sync()

		if out, err := exec.Command("mount", loop.Device, root).CombinedOutput(); err != nil {
			loop.Close()
			return nil, nil, errors.Wrapf(err, "failed to mount device %s (out: %q)", loop.Device, out)
		}

		config := &SnapshotterConfig{
			SizeMB: 50,
		}
		config.setDefaults(root)

		snapshotter, err := NewSnapshotter(ctx, config)
		if err != nil {
			mount.UnmountAll(root, 0)
			loop.Close()
			return nil, nil, errors.Wrap(err, "failed to create new snapshotter")
		}

		return snapshotter, func() error {
			if err := snapshotter.Close(); err != nil {
				return err
			}
			err := mount.UnmountAll(root, 0)
			if cerr := loop.Close(); cerr != nil {
				err = errors.Wrap(cerr, "device cleanup failed")
			}
			if err == nil {
				err = os.RemoveAll(root)
			}
			return err
		}, nil
	}
}

func TestRawblock(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Rawblock", boltSnapshotter(t))
}
