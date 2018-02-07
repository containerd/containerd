// +build !windows

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
