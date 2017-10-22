package containerd

import (
	"net"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

type dialResult struct {
	c   net.Conn
	err error
}

// Dialer returns a GRPC net.Conn connected to the provided address
func Dialer(address string, timeout time.Duration) (net.Conn, error) {
	var (
		stopC = make(chan struct{})
		synC  = make(chan *dialResult)
	)
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
		return nil, errors.Errorf("dial %s: timeout", address)
	}
}

func GetGRPCConnection(address string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts = append(opts,
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithTimeout(60*time.Second),
		grpc.FailOnNonTempDialError(true),
		grpc.WithBackoffMaxDelay(3*time.Second),
		grpc.WithDialer(Dialer))

	return grpc.Dial(DialAddress(address), opts...)
}
