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

package opts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/containerd/continuity/fs"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
)

// WithNewSnapshot wraps `containerd.WithNewSnapshot` so that if creating the
// snapshot fails we make sure the image is actually unpacked and retry.
func WithNewSnapshot(id string, i containerd.Image, opts ...snapshots.Opt) containerd.NewContainerOpts {
	f := containerd.WithNewSnapshot(id, i, opts...)
	return func(ctx context.Context, client *containerd.Client, c *containers.Container) error {
		if err := f(ctx, client, c); err != nil {
			if !errdefs.IsNotFound(err) {
				return err
			}

			if err := i.Unpack(ctx, c.Snapshotter); err != nil {
				return fmt.Errorf("error unpacking image: %w", err)
			}
			return f(ctx, client, c)
		}
		return nil
	}
}

// WithVolumes copies ownership of volume in rootfs to its corresponding host path.
// It doesn't update runtime spec.
// The passed in map is a host path to container path map for all volumes.
func WithVolumes(volumeMounts map[string]string, platform imagespec.Platform) containerd.NewContainerOpts {
	return func(ctx context.Context, client *containerd.Client, c *containers.Container) (err error) {
		if c.Snapshotter == "" {
			return errors.New("no snapshotter set for container")
		}
		if c.SnapshotKey == "" {
			return errors.New("rootfs not created for container")
		}
		snapshotter := client.SnapshotService(c.Snapshotter)
		mounts, err := snapshotter.Mounts(ctx, c.SnapshotKey)
		if err != nil {
			return err
		}
		// Since only read is needed, append ReadOnly mount option to prevent linux kernel
		// from syncing whole filesystem in umount syscall.
		if len(mounts) == 1 && mounts[0].Type == "overlay" {
			mounts[0].Options = append(mounts[0].Options, "ro")
		}

		root, err := os.MkdirTemp("", "ctd-volume")
		if err != nil {
			return err
		}
		// We change RemoveAll to Remove so that we either leak a temp dir
		// if it fails but not RM snapshot data.
		// refer to https://github.com/containerd/containerd/pull/1868
		// https://github.com/containerd/containerd/pull/1785
		defer os.Remove(root)

		if err := mount.All(mounts, root); err != nil {
			return fmt.Errorf("failed to mount: %w", err)
		}
		defer func() {
			if uerr := mount.Unmount(root, 0); uerr != nil {
				log.G(ctx).WithError(uerr).Errorf("Failed to unmount snapshot %q", root)
				if err == nil {
					err = uerr
				}
			}
		}()

		for host, volume := range volumeMounts {
			if platform.OS == "windows" {
				// Windows allows volume mounts in subfolders under C: and as any other drive letter like D:, E:, etc.
				// An image may contain files inside a folder defined as a VOLUME in a Dockerfile. On Windows, images
				// can only contain pre-existing files for volumes situated on the root filesystem, which is C:.
				// For any other volumes, we need to skip attempting to copy existing contents.
				//
				// C:\some\volume --> \some\volume
				// D:\some\volume --> skip
				if len(volume) >= 2 && string(volume[1]) == ":" {
					// Perform a case insensitive comparison to "C", and skip non-C mounted volumes.
					if !strings.EqualFold(string(volume[0]), "c") {
						continue
					}
					// This is a volume mounted somewhere under C:\. We strip the drive letter and allow fs.RootPath()
					// to append the remaining path to the rootfs path as seen by the host OS.
					volume = volume[2:]
				}
			}
			src, err := fs.RootPath(root, volume)
			if err != nil {
				return fmt.Errorf("rootpath on mountPath %s, volume %s: %w", root, volume, err)
			}
			if _, err := os.Stat(src); err != nil {
				if os.IsNotExist(err) {
					// Skip copying directory if it does not exist.
					continue
				}
				return fmt.Errorf("stat volume in rootfs: %w", err)
			}
			if err := copyExistingContents(src, host); err != nil {
				return fmt.Errorf("taking runtime copy of volume: %w", err)
			}
		}
		return nil
	}
}

// copyExistingContents copies from the source to the destination and
// ensures the ownership is appropriately set.
func copyExistingContents(source, destination string) error {
	f, err := os.Open(destination)
	if err != nil {
		return err
	}
	defer f.Close()

	dstList, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	if len(dstList) != 0 {
		return fmt.Errorf("volume at %q is not initially empty", destination)
	}
	return fs.CopyDir(destination, source, fs.WithXAttrExclude("security.selinux"))
}
