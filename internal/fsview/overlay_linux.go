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

package fsview

import (
	"io/fs"
	"os"
	"syscall"

	"github.com/erofs/go-erofs"
	"golang.org/x/sys/unix"
)

func isOpaque(f fs.File) bool {
	// Attempt to get underlying *os.File
	if osf, ok := f.(*os.File); ok {
		dest := make([]byte, 1)
		sz, err := unix.Fgetxattr(int(osf.Fd()), "trusted.overlay.opaque", dest)
		if err != nil {
			return false
		}
		return sz == 1 && dest[0] == 'y'
	}

	// Check for EROFS file by getting Stat and checking xattrs
	fi, err := f.Stat()
	if err != nil {
		return false
	}

	if estatfi, ok := fi.Sys().(*erofs.Stat); ok {
		if xattr, ok := estatfi.Xattrs["trusted.overlay.opaque"]; ok {
			return xattr == "y"
		}
	}

	return false
}

func isWhiteout(fi fs.FileInfo) bool {
	if (fi.Mode() & fs.ModeCharDevice) == 0 {
		return false
	}

	// Check for regular syscall.Stat_t (from os.File)
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		return sys.Rdev == 0
	}

	// Check for EROFS Stat
	if estatfi, ok := fi.Sys().(*erofs.Stat); ok {
		return estatfi.Rdev == 0
	}

	return false
}
