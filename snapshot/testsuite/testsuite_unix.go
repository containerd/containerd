// +build !windows

package testsuite

import (
	"syscall"

	"golang.org/x/sys/unix"
)

const umountflags int = unix.MNT_DETACH

func clearMask() func() {
	oldumask := syscall.Umask(0)
	return func() {
		syscall.Umask(oldumask)
	}
}
