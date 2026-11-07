//go:build unix

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

package proxy

import (
	"os"

	"golang.org/x/sys/unix"
)

// openRegular opens p for reading without the open itself blocking. A FIFO
// opened for read with a plain os.Open blocks until a writer appears, so a
// FIFO in the exported blobs directory would hang the caller instead of
// being rejected by the regular-file check below. O_NONBLOCK makes open
// return immediately instead, and has no effect on a regular file.
//
// The caller must still check the result with Stat; this only keeps a
// non-regular file from blocking open, not from succeeding.
func openRegular(p string) (*os.File, error) {
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	return os.NewFile(uintptr(fd), p), nil
}
