// +build !windows

package dialer

import (
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

func isNoent(err error) bool {
	if err != nil {
		if nerr, ok := err.(*net.OpError); ok {
			if serr, ok := nerr.Err.(*os.SyscallError); ok {
				if serr.Err == syscall.ENOENT {
					return true
				}
			}
		}
	}
	return false
}

func dialer(address string, timeout time.Duration) (net.Conn, error) {
	address = strings.TrimPrefix(address, "unix://")
	return net.DialTimeout("unix", address, timeout)
}
