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
// which is attached and not marked for auto clear. A symlink to a
// device is not something the host's mount table has any record of,
// so the loopback handler cannot rely on the generic check other
// handlers use and must inspect the device directly instead.
func (loopbackHandler) Mounted(ctx context.Context, path string) (bool, error) {
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

	// LO_FLAGS_AUTOCLEAR is only ever set here between Unmount
	// removing the symlink and the device actually clearing, which
	// Mounted cannot observe as a distinct state, so treat it as
	// already gone rather than live.
	if info.Flags&unix.LO_FLAGS_AUTOCLEAR != 0 {
		return false, nil
	}

	return true, nil
}

func (loopbackHandler) Unmount(ctx context.Context, path string) error {
	loopdev, err := os.Readlink(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	loop, err := os.Open(loopdev)
	if err != nil {
		return err
	}
	defer loop.Close()

	if err := setLoopAutoclear(loop, true); err != nil {
		return fmt.Errorf("failed to set auto clear on loop device %q: %w", loopdev, err)
	}

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		// if removal of the symlink has failed, its possible for the loop device to get cleaned
		// up and re-used. Leave the loop device around to prevent re-use and let a retry of
		// Unmount clear it.`
		if err := setLoopAutoclear(loop, false); err != nil {
			// Very unlikely but log to track in case there is a problem with
			// the loop being cleared and re-used.
			log.G(ctx).WithError(err).Errorf("Failed to unset auto clear flag on symlink removal failure, loopback %q may be cleaned up while still being tracked", loopdev)
		}

		return err
	}

	return nil
}
