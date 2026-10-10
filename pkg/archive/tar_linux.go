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
	"strconv"

	"golang.org/x/sys/unix"
)

// openParentFlags opens the parent directory handle used by lchmod. O_PATH
// only needs search permission on the directory, which is all the lstat and
// chmod by name it replaced needed as well; a regular O_RDONLY open would also
// require read permission and fail on e.g. a 0300 directory when not running
// as root.
const openParentFlags = unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC

// lchmodFallback changes the mode of base, relative to dirfd, when Fchmodat
// cannot honor AT_SYMLINK_NOFOLLOW: on kernels before 6.6, which lack
// fchmodat2, and on any kernel when base is a symlink.
//
// It pins base with an O_PATH|O_NOFOLLOW descriptor and, unless that is a
// symlink, applies the mode through the descriptor's /proc/self/fd entry, the
// same emulation glibc and musl use. The mode lands on the inode that was
// checked, so base cannot be swapped for a symlink in between.
func lchmodFallback(dirfd int, base string, mode os.FileMode) error {
	fd, err := unix.Openat(dirfd, base, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil
	}
	return unix.Chmod("/proc/self/fd/"+strconv.Itoa(fd), syscallMode(mode))
}
