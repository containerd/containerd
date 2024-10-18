//go:build linux

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

package os

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var (
	// Error statx support since Linux 4.11, https://man7.org/linux/man-pages/man2/statx.2.html
	errStatxNotSupport = errors.New("the statx syscall is not supported. At least Linux kernel 4.11 is needed")
)

func (RealOS) StatWithoutFileInfo(name string) error {
	_, err := statx(name)
	if err != nil {
		if errors.Is(err, errStatxNotSupport) {
			// fall back to os.stat
			_, err = os.Stat(name)
			return err
		}
		// unix.ENOTDIR means not a directory   unix.ENOENT means no such file or directory
		// unix.Statx("111") will return unix.ENOTDIR, unix.Statx("/home/111") will return unix.ENOENT
		// need trans to unix.ENOENT,os.IsNotExist will work well.
		if err == unix.ENOTDIR {
			return unix.ENOENT
		}
		return err
	}
	return nil
}

func statx(file string) (unix.Statx_t, error) {
	var stat unix.Statx_t
	if err := unix.Statx(0, file, unix.AT_STATX_DONT_SYNC, 0, &stat); err != nil {
		if err == unix.ENOSYS {
			return stat, errStatxNotSupport
		}
		return stat, err
	}

	return stat, nil
}
