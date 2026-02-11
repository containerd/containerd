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
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func copyFileWithSync(target, source string) error {
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source %s: %w", source, err)
	}
	defer src.Close()
	tgt, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to open target %s: %w", target, err)
	}
	defer tgt.Close()
	defer tgt.Sync()

	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat the source %s: %w", source, err)
	}

	srcSize := info.Size()
	if err = tgt.Truncate(srcSize); err != nil {
		return fmt.Errorf("failed to truncate the target %s: %w", target, err)
	}

	srcFd := int(src.Fd())
	tgtFd := int(tgt.Fd())
	offset := int64(0)
	for offset < srcSize {
		// search for the next hole segment
		holeStart, err := unix.Seek(srcFd, offset, unix.SEEK_HOLE)
		if err != nil {
			if errno, ok := err.(syscall.Errno); ok && errno == syscall.ENXIO {
				// no more holes found
				// copy everything from the current offset till the end of the file
				return doCopyFileRange(target, srcFd, tgtFd, &offset, int(srcSize-holeStart), 0)
			}
			return fmt.Errorf("failed to seek for holes in source %s: %w", source, err)
		}

		// this should never happen under normal conditions
		// but better be safe than sorry, in case something goes VERY wrong
		if holeStart < offset {
			return fmt.Errorf("SEEK_HOLE returned a unexpected position, most likely due to an issue with the file (%s) or the filesystem themselves", source)
		}

		// a hole was found
		if holeStart > offset {
			// copy everything from the current offset till where the hole starts
			if err := doCopyFileRange(target, srcFd, tgtFd, &offset, int(holeStart-offset), 0); err != nil {
				return err
			}
		}

		// search for the next data segment
		dataStart, err := unix.Seek(srcFd, holeStart, unix.SEEK_DATA)
		if err != nil {
			if errno, ok := err.(syscall.Errno); ok && errno == syscall.ENXIO {
				// no more data found, we reached the end of file for one of the two reasons:
				// - we actually went through the whole file
				// - the file's been created with `truncate -s ...` (or similar)
				//   - meaning that there are no data segements at all
				return nil
			}
			return fmt.Errorf("failed to seek for next data segment in source %s: %w", source, err)
		}

		// this should never happen under normal conditions
		// but, again, better be safe than sorry, in case something goes VERY wrong
		if dataStart == offset {
			return fmt.Errorf("no progress happened in this copy iteration for the file %s, indicating a unexpected error", source)
		}

		// update the offset and do everything again till we reach the end of the file
		offset = dataStart
	}
	return nil

}

func doCopyFileRange(target string, srcFd, tgtFd int, offset *int64, length int, flags uint) error {
	for length > 0 {
		copied, err := unix.CopyFileRange(srcFd, offset, tgtFd, offset, length, 0)
		if err != nil || copied == 0 {
			return fmt.Errorf("failed to copy file to target %s: %w", target, err)
		}
		length -= copied
	}
	return nil
}
