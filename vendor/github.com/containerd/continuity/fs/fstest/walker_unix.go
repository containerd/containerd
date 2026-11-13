//go:build !windows

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
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

type hardlinkKey struct {
	dev   uint64
	inode uint64
}

func statResource(fi os.FileInfo, r *resource) {
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		r.uid = int64(sys.Uid)
		r.gid = int64(sys.Gid)
	}
}

func getHardlinkKey(fi os.FileInfo) (hardlinkKey, bool) {
	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || sys.Nlink < 2 {
		return hardlinkKey{}, false
	}
	//nolint:unconvert
	return hardlinkKey{dev: uint64(sys.Dev), inode: uint64(sys.Ino)}, true
}

func getDeviceInfo(fi os.FileInfo) (major, minor uint64) {
	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	//nolint:unconvert
	dev := uint64(sys.Rdev)
	return uint64(unix.Major(dev)), uint64(unix.Minor(dev))
}
