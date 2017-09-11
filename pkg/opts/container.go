/*
Copyright 2017 The Kubernetes Authors.
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

package opts

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/docker/docker/pkg/system"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// WithVolumes copies ownership of volume in rootfs to its corresponding host path.
// It doesn't update runtime spec.
// The passed in map is a host path to container path map for all volumes.
// TODO(random-liu): Figure out whether we need to copy volume content.
func WithVolumes(volumeMounts map[string]string) containerd.NewContainerOpts {
	return func(ctx context.Context, client *containerd.Client, c *containers.Container) error {
		if c.Snapshotter == "" {
			return errors.Errorf("no snapshotter set for container")
		}
		if c.SnapshotKey == "" {
			return errors.Errorf("rootfs not created for container")
		}
		snapshotter := client.SnapshotService(c.Snapshotter)
		mounts, err := snapshotter.Mounts(ctx, c.SnapshotKey)
		if err != nil {
			return err
		}
		root, err := ioutil.TempDir("", "ctd-volume")
		if err != nil {
			return err
		}
		defer os.RemoveAll(root) // nolint: errcheck
		for _, m := range mounts {
			if err := m.Mount(root); err != nil {
				return err
			}
		}
		defer unix.Unmount(root, 0) // nolint: errcheck

		for host, volume := range volumeMounts {
			if err := copyOwnership(filepath.Join(root, volume), host); err != nil {
				return err
			}
		}
		return nil
	}
}

// copyOwnership copies the permissions and uid:gid of the src file
// to the dst file
func copyOwnership(src, dst string) error {
	stat, err := system.Stat(src)
	if err != nil {
		return err
	}

	dstStat, err := system.Stat(dst)
	if err != nil {
		return err
	}

	// In some cases, even though UID/GID match and it would effectively be a no-op,
	// this can return a permission denied error... for example if this is an NFS
	// mount.
	// Since it's not really an error that we can't chown to the same UID/GID, don't
	// even bother trying in such cases.
	if stat.UID() != dstStat.UID() || stat.GID() != dstStat.GID() {
		if err := os.Chown(dst, int(stat.UID()), int(stat.GID())); err != nil {
			return err
		}
	}

	if stat.Mode() != dstStat.Mode() {
		return os.Chmod(dst, os.FileMode(stat.Mode()))
	}
	return nil
}
