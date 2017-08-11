// +build !windows

package containerd

import (
	"fmt"
	"net"
	"os"
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
	return net.DialTimeout("unix", address, timeout)
}

func DialAddress(address string) string {
	return fmt.Sprintf("unix://%s", address)
}
