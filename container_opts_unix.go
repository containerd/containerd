// +build !windows

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

package containerd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/opencontainers/image-spec/identity"
)

// Remapper specifies the type of remapping to be done
type Remapper string

const (
	remapperChown       Remapper = "chown"
	remapperSnapshotter Remapper = "snapshotter"
)

// WithRemappedSnapshot creates a new snapshot and remaps the uid/gid for the
// filesystem to be used by a container with user namespaces
func WithRemappedSnapshot(id string, i Image, uid, gid uint32, remapper Remapper) NewContainerOpts {
	return withRemappedSnapshotBase(id, i, uid, gid, false, remapper)
}

// WithRemappedSnapshotView is similar to WithRemappedSnapshot but rootfs is mounted as read-only.
func WithRemappedSnapshotView(id string, i Image, uid, gid uint32, remapper Remapper) NewContainerOpts {
	return withRemappedSnapshotBase(id, i, uid, gid, true, remapper)
}

func withRemappedSnapshotBase(id string, i Image, uid, gid uint32, readonly bool, remapper Remapper) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		if remapper != remapperChown && remapper != remapperSnapshotter {
			return fmt.Errorf("remapper must be either '%s' or '%s'", remapperChown, remapperSnapshotter)
		}

		diffIDs, err := i.(*image).i.RootFS(ctx, client.ContentStore(), client.platform)
		if err != nil {
			return err
		}

		var (
			parent   = identity.ChainID(diffIDs).String()
			usernsID = fmt.Sprintf("%s-%d-%d", parent, uid, gid)
			opts     = []snapshots.Opt{}
		)
		c.Snapshotter, err = client.resolveSnapshotterName(ctx, c.Snapshotter)
		if err != nil {
			return err
		}
		snapshotter, err := client.getSnapshotter(ctx, c.Snapshotter)
		if err != nil {
			return err
		}
		if remapper == remapperSnapshotter {
			opts = append(opts,
				snapshots.WithLabels(map[string]string{
					"containerd.io/snapshot/uidmapping": fmt.Sprintf("%d:%d:%d", 0, uid, 65535),
					"containerd.io/snapshot/gidmapping": fmt.Sprintf("%d:%d:%d", 0, gid, 65535),
				}))
		}
		if _, err := snapshotter.Stat(ctx, usernsID); err == nil {
			if _, err := snapshotter.Prepare(ctx, id, usernsID, opts...); err == nil {
				c.SnapshotKey = id
				c.Image = i.Name()
				return nil
			} else if !errdefs.IsNotFound(err) {
				return err
			}
		}
		mounts, err := snapshotter.Prepare(ctx, usernsID+"-remap", parent, opts...)
		if err != nil {
			return err
		}
		if remapper == remapperChown {
			if err := remapRootFS(ctx, mounts, uid, gid); err != nil {
				snapshotter.Remove(ctx, usernsID)
				return err
			}
		}
		if err := snapshotter.Commit(ctx, usernsID, usernsID+"-remap", opts...); err != nil {
			return err
		}
		if readonly {
			_, err = snapshotter.View(ctx, id, usernsID, opts...)
		} else {
			_, err = snapshotter.Prepare(ctx, id, usernsID, opts...)
		}
		if err != nil {
			return err
		}
		c.SnapshotKey = id
		c.Image = i.Name()
		return nil
	}
}

func remapRootFS(ctx context.Context, mounts []mount.Mount, uid, gid uint32) error {
	return mount.WithTempMount(ctx, mounts, func(root string) error {
		return filepath.Walk(root, incrementFS(root, uid, gid))
	})
}

func incrementFS(root string, uidInc, gidInc uint32) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		var (
			stat = info.Sys().(*syscall.Stat_t)
			u, g = int(stat.Uid + uidInc), int(stat.Gid + gidInc)
		)
		// be sure the lchown the path as to not de-reference the symlink to a host file
		return os.Lchown(path, u, g)
	}
}
