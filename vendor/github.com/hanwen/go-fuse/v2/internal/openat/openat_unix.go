//go:build !linux

package openat

import (
	"golang.org/x/sys/unix"
)

// TODO: This is insecure as O_NOFOLLOW only affects the final path component.
// See https://github.com/rfjakob/gocryptfs/issues/165 for how this could be handled.
func openatNoSymlinks(dirfd int, path string, flags int, mode uint32) (fd int, err error) {
	// os/exec expects all fds to have O_CLOEXEC or it will leak them to subprocesses.
	flags |= unix.O_NOFOLLOW | unix.O_CLOEXEC
	return unix.Openat(dirfd, path, flags, mode)
}
