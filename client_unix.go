// +build !windows

package containerd

import (
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
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

type dialResult struct {
	c   net.Conn
	err error
}

func dialer(address string, timeout time.Duration) (net.Conn, error) {
	var (
		stopC = make(chan struct{})
		synC  = make(chan *dialResult)
	)
	address = strings.TrimPrefix(address, "unix://")
	go func() {
		defer close(synC)
		for {
			select {
			case <-stopC:
				return
			default:
				c, err := net.DialTimeout("unix", address, timeout)
				if isNoent(err) {
					<-time.After(10 * time.Millisecond)
					continue
				}
				synC <- &dialResult{c, err}
				return
			}
		}
	}()
	select {
	case dr := <-synC:
		return dr.c, dr.err
	case <-time.After(timeout):
		close(stopC)
		go func() {
			dr := <-synC
			if dr != nil {
				dr.c.Close()
			}
		}()
		return nil, errors.Errorf("dial %s: no such file or directory", address)
	}
}

func dialAddress(address string) string {
	return fmt.Sprintf("unix://%s", address)
}
