package containerd

import (
	"net"
	"time"

	winio "github.com/Microsoft/go-winio"
)

func Dialer(address string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(address, &timeout)
}

func DialAddress(address string) string {
	return address
}
