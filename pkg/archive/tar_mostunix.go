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
	"path/filepath"

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

// lchmod changes the mode of path without following it when path is a symlink.
//
// It opens a handle to path's parent directory and operates on the base name
// relative to that handle, so the directory components of path are resolved
// once and cannot be redirected afterwards. The chmod itself uses Fchmodat
// with AT_SYMLINK_NOFOLLOW so it never follows a terminal symlink, matching
// the freebsd build.
//
// Fchmodat reports EOPNOTSUPP when it cannot honor the flag. On Linux that is
// the case on kernels before 6.6, which lack fchmodat2 (the x/sys/unix wrapper
// translates the ENOSYS), and on any kernel when path itself is a symlink,
// since symlinks have no mode of their own there. Other unixes implement the
// flag natively, but some of them, Solaris for one, still report it for a
// symlink. All of these go through the platform-specific lchmodFallback,
// which leaves symlinks alone.
func lchmod(path string, mode os.FileMode) error {
	dirfd, err := unix.Open(filepath.Dir(path), openParentFlags, 0)
	if err != nil {
		return &os.PathError{Op: "lchmod", Path: path, Err: err}
	}
	defer unix.Close(dirfd)

	base := filepath.Base(path)
	err = unix.Fchmodat(dirfd, base, syscallMode(mode), unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.EOPNOTSUPP) {
		err = lchmodFallback(dirfd, base, mode)
	}
	if err != nil {
		return &os.PathError{Op: "lchmod", Path: path, Err: err}
	}
	return nil
}
