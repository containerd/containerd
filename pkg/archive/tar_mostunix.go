//go:build !windows && !freebsd

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
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// mknod wraps Unix.Mknod and casts dev to int
func mknod(path string, mode uint32, dev uint64) error {
	return unix.Mknod(path, mode, int(dev))
}

// lsetxattrCreate wraps unix.Lsetxattr, passes the unix.XATTR_CREATE flag on
// supported operating systems,and ignores appropriate errors
func lsetxattrCreate(link string, attr string, data []byte) error {
	err := unix.Lsetxattr(link, attr, data, unix.XATTR_CREATE)
	if err == unix.ENOTSUP || err == unix.ENODATA || err == unix.EEXIST {
		return nil
	}
	return err
}

// lchmod changes the mode of the file at path without following symlinks; a
// symlink is intentionally left unmodified.
func lchmod(path string, mode os.FileMode) error {
	// Fchmodat with AT_SYMLINK_NOFOLLOW targets the inode at path directly,
	// removing the TOCTOU window that the previous lstat-then-chmod had: an
	// attacker racing the extraction could replace a just-created file with a
	// symlink between the check and the chmod, and the old path-based chmod
	// would then follow it (cf. ba50a56, which fixed the same race in openFile).
	//
	// mode carries os.FileMode's Go-style setuid/setgid/sticky bits, which do
	// not match the syscall bits, so convert it the way os.Chmod would rather
	// than casting directly.
	err := unix.Fchmodat(unix.AT_FDCWD, path, chmodSyscallMode(mode), unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EOPNOTSUPP) {
		// AT_SYMLINK_NOFOLLOW yields EOPNOTSUPP in two situations:
		//   - path is a symlink: Linux cannot change a symlink's own mode, and
		//     the historical behaviour is to leave symlinks untouched, so this
		//     is success; or
		//   - the running kernel predates fchmodat2 (Linux < 6.6) and cannot
		//     honour the flag for any path type. Fall back to the previous
		//     lstat-guarded chmod, preserving behaviour on such kernels.
		fi, lerr := os.Lstat(path)
		if lerr != nil {
			return lerr
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return os.Chmod(path, mode)
	}
	return err
}

// chmodSyscallMode converts an os.FileMode into the raw mode bits accepted by
// chmod(2)/fchmodat(2). It mirrors the unexported os.syscallMode so that the
// setuid, setgid and sticky bits survive; a plain uint32(mode) would drop them.
func chmodSyscallMode(i os.FileMode) uint32 {
	o := uint32(i.Perm())
	if i&os.ModeSetuid != 0 {
		o |= unix.S_ISUID
	}
	if i&os.ModeSetgid != 0 {
		o |= unix.S_ISGID
	}
	if i&os.ModeSticky != 0 {
		o |= unix.S_ISVTX
	}
	return o
}
