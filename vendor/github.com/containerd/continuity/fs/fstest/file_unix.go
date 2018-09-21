// +build !windows

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

package fstest

import (
	"path/filepath"
	"time"

	"github.com/containerd/continuity/sysx"
	"golang.org/x/sys/unix"
)

// SetXAttr sets the xatter for the file
func SetXAttr(name, key, value string) Applier {
	return applyFn(func(root string) error {
		return sysx.LSetxattr(name, key, []byte(value), 0)
	})
}

// Lchtimes changes access and mod time of file without following symlink
func Lchtimes(name string, atime, mtime time.Time) Applier {
	return applyFn(func(root string) error {
		path := filepath.Join(root, name)
		at := unix.NsecToTimespec(atime.UnixNano())
		mt := unix.NsecToTimespec(mtime.UnixNano())
		utimes := [2]unix.Timespec{at, mt}
		return unix.UtimesNanoAt(unix.AT_FDCWD, path, utimes[0:], unix.AT_SYMLINK_NOFOLLOW)
	})
}

func Base() Applier {
	return applyFn(func(root string) error {
		// do nothing, as the base is not special
		return nil
	})
}
