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
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"golang.org/x/sys/unix"
)

func LoopbackHandler() Handler {
	return loopbackHandler{}
}

type loopbackHandler struct {
}

func (loopbackHandler) Mount(ctx context.Context, m Mount, mp string, _ []ActiveMount) (ActiveMount, error) {
	if m.Type != "loop" {
		return ActiveMount{}, errdefs.ErrNotImplemented
	}
	params := LoopParams{
		Autoclear: true,
	}
	// TODO: Handle readonly
	// TODO: Handle direct io

	t := time.Now()
	loop, err := SetupLoop(m.Source, params)
	if err != nil {
		return ActiveMount{}, err
	}
	defer loop.Close()

	// A stale symlink from an earlier, no longer live mount may already
	// be at mp (see Mounted); os.Symlink fails with EEXIST otherwise.
	// Remove it first, but only if it really is a symlink.
	if fi, err := os.Lstat(mp); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(mp); err != nil {
			return ActiveMount{}, err
		}
	}

	if err := os.Symlink(loop.Name(), mp); err != nil {
		return ActiveMount{}, err
	}

	if err := setLoopAutoclear(loop, false); err != nil {
		return ActiveMount{}, err
	}

	return ActiveMount{
		Mount:      m,
		MountedAt:  &t,
		MountPoint: mp,
	}, nil
}

// Mounted reports whether path is still a symlink to a loop device
// which is attached to m.Source and not marked for auto clear. A
// device reattached to a different backing file is live but is not
// m's mount, and is reported as not mounted.
func (loopbackHandler) Mounted(ctx context.Context, m Mount, path string) (bool, error) {
	loopdev, err := os.Readlink(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	loop, err := os.Open(loopdev)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer loop.Close()

	info, err := unix.IoctlLoopGetStatus64(int(loop.Fd()))
	if err != nil {
		// ENXIO: no backing file is attached to the device, so
		// whatever this symlink once pointed to is gone.
		if errors.Is(err, unix.ENXIO) {
			return false, nil
		}
		return false, err
	}

	if info.Flags&unix.LO_FLAGS_AUTOCLEAR != 0 {
		return false, nil
	}

	return backsSource(info, m.Source)
}

// backsSource reports whether info describes a loop device attached to
// source. lo_device and lo_inode identify the backing file itself;
// lo_file_name cannot, being a fixed 64 byte field the paths here
// routinely outgrow. Both are zero when the kernel could not stat the
// backing file, leaving nothing to compare against.
func backsSource(info *unix.LoopInfo64, source string) (bool, error) {
	if source == "" || (info.Device == 0 && info.Inode == 0) {
		return true, nil
	}
	var st unix.Stat_t
	if err := unix.Stat(source, &st); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false, nil
		}
		return false, err
	}
	return info.Inode == st.Ino && info.Device == encodeDev(st.Dev), nil
}

// encodeDev encodes a device number the way the loop ioctls report it,
// matching the kernel's own new_encode_dev.
func encodeDev(dev uint64) uint64 {
	major := uint64(unix.Major(dev))
	minor := uint64(unix.Minor(dev))
	return (minor & 0xff) | (major << 8) | ((minor & ^uint64(0xff)) << 12)
}

// Unmount tolerates the loop device already being gone, whether the
// device node is missing or IoctlLoopGetStatus64 reports ENXIO.
func (loopbackHandler) Unmount(ctx context.Context, path string) error {
	loopdev, err := os.Readlink(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	loop, err := os.Open(loopdev)
	switch {
	case err == nil:
		defer loop.Close()
		if err := setLoopAutoclear(loop, true); err != nil && !errors.Is(err, unix.ENXIO) {
			return fmt.Errorf("failed to set auto clear on loop device %q: %w", loopdev, err)
		}
	case os.IsNotExist(err):
	default:
		return err
	}

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		// if removal of the symlink has failed, its possible for the loop device to get cleaned
		// up and re-used. Leave the loop device around to prevent re-use and let a retry of
		// Unmount clear it.`
		if loop != nil {
			if err := setLoopAutoclear(loop, false); err != nil {
				// Very unlikely but log to track in case there is a problem with
				// the loop being cleared and re-used.
				log.G(ctx).WithError(err).Errorf("Failed to unset auto clear flag on symlink removal failure, loopback %q may be cleaned up while still being tracked", loopdev)
			}
		}

		return err
	}

	return nil
}
