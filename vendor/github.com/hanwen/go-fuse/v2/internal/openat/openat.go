package openat

import (
	"golang.org/x/sys/unix"
)

// OpenSymlinkAware is a symlink-aware syscall.Open replacement.
//
// What it does:
//
//  1. Open baseDir (usually an absolute path), following symlinks.
//
//     The user may have set up the directory tree with symlinks,
//     that's not neccessarily malicous, but a normal use case.
//
//  2. Open path (must be a relative path) within baseDir, rejecting symlinks with ELOOP.
//
//     On Linux, it calls openat2(2) with RESOLVE_NO_SYMLINKS. This prevents following
//     symlinks in any component of the path.
//
//     On other platforms, it calls openat(2) with O_NOFOLLOW.
//     TODO: This is insecure as O_NOFOLLOW only affects the final path component.
func OpenSymlinkAware(baseDir string, path string, flags int, mode uint32) (fd int, err error) {
	// Passing an absolute path is a bug in the caller
	if len(path) > 0 && path[0] == '/' {
		return -1, unix.EINVAL
	}
	baseFd, err := unix.Open(baseDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(baseFd)

	return openatNoSymlinks(baseFd, path, flags, mode)
}
