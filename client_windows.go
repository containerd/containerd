package containerd

import (
	"net"
	"time"

	winio "github.com/Microsoft/go-winio"
)

func dialer(address string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(address, &timeout)
}

func dialAddress(address string) string {
	return address
}
