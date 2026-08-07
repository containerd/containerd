//go:build linux

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
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// copyFile copies a file from source to target preserving sparse file holes.
//
// If the filesystem does not support SEEK_DATA/SEEK_HOLE, it falls back
// to a plain io.Copy.
func copyFileWithSync(target, source string) error {
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source %s: %w", source, err)
	}
	defer src.Close()

	fi, err := src.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", source, err)
	}
	size := fi.Size()

	tgt, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to open target %s: %w", target, err)
	}
	defer tgt.Close()
	defer tgt.Sync()

	if err := tgt.Truncate(size); err != nil {
		return fmt.Errorf("failed to truncate target %s: %w", target, err)
	}

	srcFd := int(src.Fd())

	// Try a SEEK_DATA to check if the filesystem supports it.
	// If not, fall back to a plain copy.
	if _, err := unix.Seek(srcFd, 0, unix.SEEK_DATA); err != nil {
		// ENXIO means no data in the file at all. In other words it's entirely sparse.
		// The truncated target is already correct.
		if errors.Is(err, syscall.ENXIO) {
			return nil
		}

		if errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOTSUP) {
			// Filesystem doesn't support SEEK_DATA/SEEK_HOLE. Funnily enough the go stdlib
			// calls copy_file_range for this also, but it just doesn't handle sparseness
			// like we try to in this function.
			_, copyErr := io.Copy(tgt, src)
			return copyErr
		}

		return fmt.Errorf("failed to seek data in source %s: %w", source, err)
	}

	// Copy data regions from source to target, skipping holes.
	var offset int64
	tgtFd := int(tgt.Fd())

	for offset < size {
		dataStart, err := unix.Seek(srcFd, offset, unix.SEEK_DATA)
		if err != nil {
			// No more data past offset. Remainder of file is a hole.
			if errors.Is(err, syscall.ENXIO) {
				break
			}
			return fmt.Errorf("SEEK_DATA failed at offset %d: %w", offset, err)
		}

		// Find the end of this data region (start of next hole).
		holeStart, err := unix.Seek(srcFd, dataStart, unix.SEEK_HOLE)
		if err != nil {
			// ENXIO shouldn't happen after a successful SEEK_DATA, but
			// treat it as data extending to end of file.
			if errors.Is(err, syscall.ENXIO) {
				holeStart = size
			} else {
				return fmt.Errorf("SEEK_HOLE failed at offset %d: %w", dataStart, err)
			}
		}

		// Copy the data region [dataStart, holeStart).
		srcOff := dataStart
		tgtOff := dataStart
		remain := holeStart - dataStart

		for remain > 0 {
			n, err := unix.CopyFileRange(srcFd, &srcOff, tgtFd, &tgtOff, int(remain), 0)
			if err != nil {
				return fmt.Errorf("copy_file_range failed: %w", err)
			}
			if n == 0 {
				break
			}
			remain -= int64(n)
		}

		offset = holeStart
	}

	return nil
}
