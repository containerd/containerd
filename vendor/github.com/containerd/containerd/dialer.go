package containerd

import (
	"net"
	"strings"
	"time"

	"github.com/pkg/errors"
)

type dialResult struct {
	c   net.Conn
	err error
}

func Dialer(address string, timeout time.Duration) (net.Conn, error) {
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
				c, err := dialer(address, timeout)
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
