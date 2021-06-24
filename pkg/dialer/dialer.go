/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package dialer

import (
	"context"
	stderrs "errors"
	"net"
	"time"

	"github.com/pkg/errors"
)

type dialResult struct {
	c   net.Conn
	err error
}

var (
	// ErrTimeout represents a timeout error while dialing
	ErrTimeout = stderrs.New("timeout")
)

// ContextDialer returns a GRPC net.Conn connected to the provided address.
// It tolerates ENOENT and keeps retrying if provided context has a deadline.
// It does a "one shot" connect attempt if provided context doesn't have a deadline.
func ContextDialer(ctx context.Context, address string) (net.Conn, error) {
	if _, ok := ctx.Deadline(); !ok {
		return contextDialer(ctx, address, nil)
	}
	return contextDialer(ctx, address, isNoent)
}

// Dialer returns a GRPC net.Conn connected to the provided address
// Deprecated: use ContextDialer and grpc.WithContextDialer.
var Dialer = timeoutDialer

// timeoutDialer connects to the provided address with a timeout.
func timeoutDialer(address string, timeout time.Duration) (conn net.Conn, err error) {
	if timeout == 0 {
		return dialer(address, timeout)
	}

	timeoutCtx, _ := context.WithTimeout(context.TODO(), timeout)
	return contextDialer(timeoutCtx, address, isNoent)
}

// contextDialer connects to the provided address with a context.
// It may tolerate certain errors and keep retrying before context canceled.
// It returns immediately on context cancellation.
func contextDialer(ctx context.Context, address string, tolerateErr func(error) bool) (net.Conn, error) {
	stopCh := make(chan struct{})
	resCh := make(chan *dialResult, 1)

	go func() {
		defer close(resCh)
		for {
			select {
			case <-stopCh:
				return
			default:
				// Limit a single connect attempt timeout to 10s
				c, err := dialer(address, 10*time.Second)
				if tolerateErr != nil && tolerateErr(err) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				resCh <- &dialResult{c: c, err: err}
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		close(stopCh)
		go func() {
			// If the dial succeed after timeout,
			// close it to prevent resource leak.
			r := <-resCh
			if r != nil && r.c != nil {
				r.c.Close()
			}
		}()
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.Wrapf(ErrTimeout, "dial %s", address)
		}
		return nil, errors.Wrapf(ctx.Err(), "dial %s", address)
	case res := <-resCh:
		return res.c, res.err
	}
}
