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
// On Linux the flag needs fchmodat2 (kernel 6.6+): on older kernels the
// x/sys/unix wrapper translates the fchmodat2 ENOSYS into EOPNOTSUPP, and
// kernels that do have it return EOPNOTSUPP when path itself is a symlink,
// since symlinks have no modes of their own on Linux. Both cases fall back to
// an fstatat-guarded chmod relative to the same directory handle. That
// fallback is not atomic: the base name can still be swapped for a symlink
// between the fstatat and the chmod, so pre-6.6 Linux kernels keep a narrow
// window on the final component. Other unixes support the flag natively and
// never take the fallback.
func lchmod(path string, mode os.FileMode) error {
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return &os.PathError{Op: "lchmod", Path: path, Err: err}
	}
	defer parent.Close()

	base := filepath.Base(path)
	dirfd := int(parent.Fd())

	err = unix.Fchmodat(dirfd, base, syscallMode(mode), unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, unix.EOPNOTSUPP):
		return &os.PathError{Op: "lchmod", Path: path, Err: err}
	}

	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return &os.PathError{Op: "lchmod", Path: path, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFLNK {
		if err := unix.Fchmodat(dirfd, base, syscallMode(mode), 0); err != nil {
			return &os.PathError{Op: "lchmod", Path: path, Err: err}
		}
	}
	return nil
}

// syscallMode returns the Unix permission bits (including the setuid, setgid
// and sticky bits) for an os.FileMode, matching the translation os.Chmod does
// internally before calling the kernel.
func syscallMode(i os.FileMode) (o uint32) {
	o |= uint32(i.Perm())
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
