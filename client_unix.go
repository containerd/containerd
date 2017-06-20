// +build !windows

package containerd

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// DefaultAddress is the default unix socket address
const DefaultAddress = "/run/containerd/containerd.sock"

func dialer(address string, timeout time.Duration) (net.Conn, error) {
	address = strings.TrimPrefix(address, "unix://")
	return net.DialTimeout("unix", address, timeout)
}

func dialAddress(address string) string {
	return fmt.Sprintf("unix://%s", address)
}
