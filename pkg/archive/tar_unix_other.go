//go:build !windows && !freebsd && !linux

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

package archive

import (
	"os"

	"golang.org/x/sys/unix"
)

// openParentFlags opens the parent directory handle used by lchmod. These
// platforms have no search-only open flag in x/sys/unix, so like os.Root this
// needs read permission on the directory.
const openParentFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC

// lchmodFallback changes the mode of base, relative to dirfd, when Fchmodat
// reports EOPNOTSUPP for AT_SYMLINK_NOFOLLOW. These platforms implement the
// flag natively, so that only happens where a symlink's mode cannot be
// changed (Solaris is one), and the symlink is simply left alone. Anything
// else is chmod'ed by name after an fstatat check, which is not atomic, but
// in practice this path is not reached for non-symlinks.
func lchmodFallback(dirfd int, base string, mode os.FileMode) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil
	}
	return unix.Fchmodat(dirfd, base, syscallMode(mode), 0)
}
