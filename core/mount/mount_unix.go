//go:build !windows && !openbsd

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

package mount

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/moby/sys/mountinfo"
	"golang.org/x/sys/unix"
)

// UnmountRecursive unmounts the target and all mounts underneath, starting
// with the deepest mount first.
func UnmountRecursive(target string, flags int) error {
	if target == "" {
		return nil
	}

	target, err := CanonicalizePath(target)
	if err != nil {
		if os.IsNotExist(err) {
			err = nil
		}
		return err
	}

	mounts, err := mountinfo.GetMounts(mountinfo.PrefixFilter(target))
	if err != nil {
		return err
	}

	targetSet := make(map[string]struct{})
	for _, m := range mounts {
		targetSet[m.Mountpoint] = struct{}{}
	}

	var targets []string
	for m := range targetSet {
		targets = append(targets, m)
	}

	// Make the deepest mount be first
	sort.SliceStable(targets, func(i, j int) bool {
		return len(targets[i]) > len(targets[j])
	})

	for i, target := range targets {
		if err := UnmountAll(target, flags); err != nil {
			if i == len(targets)-1 { // last mount
				return err
			}
		}
	}
	return nil
}

// unmount tries to unmount a filesystem at the target path.
// If the target is a FUSE filesystem, it uses unmountFUSE.
// If unmountFUSE fails, the function returns an error without calling unix.Unmount.
//
// The reason for not attempting unix.Unmount after unmountFUSE failure is that
// FUSE filesystems are managed by userspace and require specific handling
// via FUSE tools (e.g., fusermount). Standard system calls like unix.Unmount
// do not work for FUSE filesystems, making such a call redundant and ineffective.
func unmount(target string, flags int) error {
	// Check if the target is a FUSE filesystem.
	if isFUSE(target) {
		// Try to unmount using FUSE-specific logic.
		if err := unmountFUSE(target); err != nil {
			return err
		}
		return nil
	}

	// Retry unmounting up to 50 times in case of EBUSY.
	const maxRetries = 50
	const retryDelay = 50 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		if err := unix.Unmount(target, flags); err != nil {
			if err == unix.EBUSY {
				// If target is busy, wait and retry.
				time.Sleep(retryDelay)
				continue
			}
			// Return the error for any other failure.
			return fmt.Errorf("failed to unmount target %s: %w", target, err)
		}
		// Unmount successful.
		return nil
	}

	// If all retries failed due to EBUSY, return an error.
	return fmt.Errorf("failed to unmount target %s after %d retries: %w", target, maxRetries, unix.EBUSY)
}

// Unmount the provided mount path with the flags
func Unmount(target string, flags int) error {
	if err := unmount(target, flags); err != nil && err != unix.EINVAL {
		return err
	}
	return nil
}

// UnmountAll repeatedly unmounts the given mount point until there
// are no mounts remaining (EINVAL is returned by mount), which is
// useful for undoing a stack of mounts on the same mount point.
// UnmountAll all is noop when the first argument is an empty string.
// This is done when the containerd client did not specify any rootfs
// mounts (e.g. because the rootfs is managed outside containerd)
// UnmountAll is noop when the mount path does not exist.
func UnmountAll(mount string, flags int) error {
	if mount == "" {
		return nil
	}
	if _, err := os.Stat(mount); os.IsNotExist(err) {
		return nil
	}

	for {
		if err := unmount(mount, flags); err != nil {
			// EINVAL is returned if the target is not a
			// mount point, indicating that we are
			// done. It can also indicate a few other
			// things (such as invalid flags) which we
			// unfortunately end up squelching here too.
			if err == unix.EINVAL {
				return nil
			}
			return err
		}
	}
}
