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

//go:build linux

package utils

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// GetBlockOrFileSize returns the size of the file or block device at path.
// For block devices the BLKGETSIZE64 ioctl is used; for regular files
// os.Stat is used. Block device support is required for the CLI which
// operates on real disk partitions.
func GetBlockOrFileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if st.Mode()&os.ModeDevice != 0 && st.Mode()&os.ModeCharDevice == 0 {
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		var size uint64
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.BLKGETSIZE64, uintptr(unsafe.Pointer(&size)))
		if errno != 0 {
			return 0, errno
		}
		return int64(size), nil
	}
	return st.Size(), nil
}
