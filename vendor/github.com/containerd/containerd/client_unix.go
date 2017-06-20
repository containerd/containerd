// +build !windows

package containerd

import (
	"fmt"
	"net"
	"strings"
	"time"
)

func dialer(address string, timeout time.Duration) (net.Conn, error) {
	address = strings.TrimPrefix(address, "unix://")
	return net.DialTimeout("unix", address, timeout)
}

func dialAddress(address string) string {
	return fmt.Sprintf("unix://%s", address)
}
