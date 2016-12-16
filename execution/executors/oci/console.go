package oci

import (
	"os"
	"syscall"
)

// newConsole returns an initialized console that can be used within a container by copying bytes
// from the master side to the slave that is attached as the tty for the container's init process.
func newConsole(uid, gid int) (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	console, err := ptsname(master)
	if err != nil {
		return nil, "", err
	}
	if err := unlockpt(master); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(console, 0600); err != nil {
		return nil, "", err
	}
	if err := os.Chown(console, uid, gid); err != nil {
		return nil, "", err
	}
	return master, console, nil
}
