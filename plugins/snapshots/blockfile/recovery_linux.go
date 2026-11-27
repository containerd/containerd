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
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/containerd/errdefs"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/core/mount"
)

// recoverExt4View replays an offline journal on a private copy. The committed
// image can still be in use by other mounts or a VM.
func recoverExt4View(ctx context.Context, source, target string) (recovered bool, retErr error) {
	src, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer src.Close()

	needed, err := ext4NeedsRecovery(src)
	if err != nil || !needed {
		return false, err
	}

	if err := leaseExt4Source(ctx, src.Fd()); err != nil {
		return false, err
	}
	// A lazy unmount can finish and clear the recovery bit before the lease
	// becomes available. In that case the original image can be mounted.
	needed, err = ext4NeedsRecovery(src)
	if err != nil || !needed {
		return false, err
	}

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false, err
	}
	defer func() {
		if retErr != nil {
			os.Remove(target)
		}
	}()
	_, copyErr := io.Copy(dst, src)
	if copyErr == nil {
		copyErr = dst.Sync()
	}
	if err := errors.Join(copyErr, dst.Close()); err != nil {
		return false, err
	}

	// If a writer tried to break the lease, the copy may have overlapped
	// writes after the kernel's lease-break timeout.
	lease, err := unix.FcntlInt(src.Fd(), unix.F_GETLEASE, 0)
	if err != nil {
		return false, err
	}
	if lease != unix.F_RDLCK {
		return false, fmt.Errorf("backing file was opened for writing while being copied: %w", errdefs.ErrUnavailable)
	}
	if err := src.Close(); err != nil {
		return false, err
	}

	m := mount.Mount{
		Type:    "ext4",
		Source:  target,
		Options: []string{"loop", "rw"},
	}
	if err := mount.WithTempMount(ctx, []mount.Mount{m}, func(string) error { return nil }); err != nil {
		return false, err
	}
	return true, nil
}

func ext4NeedsRecovery(src *os.File) (bool, error) {
	const (
		superblockOffset  = 1024
		magicOffset       = 0x38
		incompatOffset    = 0x60
		journalInumOffset = 0xe0
		ext4Magic         = 0xef53
		needsRecovery     = 0x0004
	)
	var superblock [journalInumOffset + 4]byte
	if _, err := src.ReadAt(superblock[:], superblockOffset); err != nil {
		return false, err
	}
	if binary.LittleEndian.Uint16(superblock[magicOffset:]) != ext4Magic {
		return false, nil
	}
	if binary.LittleEndian.Uint32(superblock[incompatOffset:])&needsRecovery == 0 {
		return false, nil
	}
	if binary.LittleEndian.Uint32(superblock[journalInumOffset:]) == 0 {
		return false, fmt.Errorf("backing file needs recovery with an external journal: %w", errdefs.ErrFailedPrecondition)
	}
	return true, nil
}

func leaseExt4Source(ctx context.Context, fd uintptr) error {
	// The copy checks the lease state instead of handling lease-break signals.
	// Use a signal that the process can safely ignore.
	if _, err := unix.FcntlInt(fd, unix.F_SETSIG, int(unix.SIGWINCH)); err != nil {
		return err
	}

	// A detached mount may still be finishing writeback. Wait briefly for
	// the writer to close before trying to copy the image.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := unix.FcntlInt(fd, unix.F_SETLEASE, unix.F_RDLCK); err == nil {
			return nil
		} else if !errors.Is(err, unix.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("backing file is still open for writing: %w", errors.Join(errdefs.ErrUnavailable, ctx.Err()))
		case <-tick.C:
		}
	}
}
