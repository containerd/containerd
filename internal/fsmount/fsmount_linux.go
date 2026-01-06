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

package fsmount

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/core/mount"
)

var mountAttrFlags = map[string]struct {
	clear bool
	flag  int
}{
	"ro":            {false, unix.MOUNT_ATTR_RDONLY},
	"rw":            {true, unix.MOUNT_ATTR_RDONLY},
	"nosuid":        {false, unix.MOUNT_ATTR_NOSUID},
	"suid":          {true, unix.MOUNT_ATTR_NOSUID},
	"nodev":         {false, unix.MOUNT_ATTR_NODEV},
	"dev":           {true, unix.MOUNT_ATTR_NODEV},
	"noexec":        {false, unix.MOUNT_ATTR_NOEXEC},
	"exec":          {true, unix.MOUNT_ATTR_NOEXEC},
	"noatime":       {false, unix.MOUNT_ATTR_NOATIME},
	"atime":         {true, unix.MOUNT_ATTR_NOATIME},
	"nodiratime":    {false, unix.MOUNT_ATTR_NODIRATIME},
	"diratime":      {true, unix.MOUNT_ATTR_NODIRATIME},
	"relatime":      {false, unix.MOUNT_ATTR_RELATIME},
	"norelatime":    {true, unix.MOUNT_ATTR_RELATIME},
	"strictatime":   {false, unix.MOUNT_ATTR_STRICTATIME},
	"nostrictatime": {true, unix.MOUNT_ATTR_STRICTATIME},
}

// Fsopen opens a filesystem context for configuration.
func Fsopen(fsName string, flags int) (*os.File, error) {
	flags |= unix.FSOPEN_CLOEXEC
	fd, err := unix.Fsopen(fsName, flags)
	if err != nil {
		return nil, os.NewSyscallError("fsopen "+fsName, err)
	}
	return os.NewFile(uintptr(fd), "fscontext:"+fsName), nil
}

// fsmount creates a mount fd from a filesystem context.
func fsmount(fsctx *os.File, flags, mountAttrs int) (*os.File, error) {
	flags |= unix.FSMOUNT_CLOEXEC
	fd, err := unix.Fsmount(int(fsctx.Fd()), flags, mountAttrs)
	if err != nil {
		return nil, os.NewSyscallError("fsmount "+fsctx.Name(), err)
	}
	return os.NewFile(uintptr(fd), "fsmount:"+fsctx.Name()), nil
}

// SupportsFsmount checks if the fsmount syscall is available (Linux 5.2+).
func SupportsFsmount() bool {
	fd, err := unix.Fsopen("__nonexistent__", unix.FSOPEN_CLOEXEC)
	if err == unix.ENOSYS {
		return false
	}
	if fd >= 0 {
		unix.Close(fd)
	}
	return true
}

// Fsmount mounts the filesystem using the new mount API (fsopen/fsconfig/fsmount/move_mount).
// This approach avoids the PAGE_SIZE limitation of traditional mount() syscall by setting
// options individually via fsconfig() instead of passing them as a single string.
func Fsmount(m mount.Mount, target string) error {
	fsctx, err := Fsopen(m.Type, 0)
	if err != nil {
		return err
	}
	defer fsctx.Close()

	// Check if "ro" option is present - must be set before source for read-only loop devices
	roFlag := false
	for _, o := range m.Options {
		if o == "ro" {
			roFlag = true
			break
		}
	}
	if roFlag {
		if err := unix.FsconfigSetFlag(int(fsctx.Fd()), "ro"); err != nil {
			return fmt.Errorf("failed to set ro flag: %w", err)
		}
	}

	if err := unix.FsconfigSetString(int(fsctx.Fd()), "source", m.Source); err != nil {
		return fmt.Errorf("failed to set source: %w", err)
	}

	var mountAttrs int
	for _, o := range m.Options {
		if f, ok := mountAttrFlags[o]; ok {
			if f.clear {
				mountAttrs &^= f.flag
			} else {
				mountAttrs |= f.flag
			}
			continue
		}

		// Handle key=value options
		if key, val, ok := strings.Cut(o, "="); ok {
			if err := unix.FsconfigSetString(int(fsctx.Fd()), key, val); err != nil {
				return fmt.Errorf("failed to set string option %s=%s: %w", key, val, err)
			}
			continue
		}

		// Handle filesystem-specific flags
		if err := unix.FsconfigSetFlag(int(fsctx.Fd()), o); err != nil {
			return fmt.Errorf("failed to set flag %s: %w", o, err)
		}
	}

	if err := unix.FsconfigCreate(int(fsctx.Fd())); err != nil {
		return fmt.Errorf("failed to create fs: %w", err)
	}

	mfd, err := fsmount(fsctx, 0, mountAttrs)
	if err != nil {
		return fmt.Errorf("failed to fsmount: %w", err)
	}
	defer mfd.Close()

	if err := unix.MoveMount(int(mfd.Fd()), "", unix.AT_FDCWD, target, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
		return fmt.Errorf("failed to move mount: %w", err)
	}
	return nil
}
