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
	"crypto/md5"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestContextDialer(t *testing.T) {
	ts := []struct {
		Name      string
		Server    func(string, <-chan struct{}) (net.Addr, error)
		Timeout   time.Duration
		Cancel    bool
		ExpectErr bool
		Err       error
	}{
		{
			Name:      "dial successful",
			Server:    newEchoServer,
			ExpectErr: false,
		},
		{
			Name:      "context canceled",
			Server:    newBlockServer,
			Cancel:    true,
			ExpectErr: true,
			Err:       context.Canceled,
		},
		{
			Name:      "dial timeout",
			Server:    newBlockServer,
			Timeout:   300 * time.Millisecond,
			ExpectErr: true,
			Err:       ErrTimeout,
		},
	}
	t.Parallel()
	for i := range ts {
		tt := ts[i]
		t.Run(tt.Name, func(t *testing.T) {
			stop := make(chan struct{})
			addr, err := tt.Server(tt.Name, stop)
			if err != nil {
				t.Fatalf("failed to start test server: %v", err)
			}
			defer close(stop)
			if tt.Timeout == 0 {
				tt.Timeout = 3 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.TODO(), tt.Timeout)
			defer cancel()
			if tt.Cancel {
				go func() {
					time.Sleep(300 * time.Millisecond)
				}()
				cancel()
			}
			_, err = ContextDialer(ctx, addr.String())
			if err != nil {
				if tt.ExpectErr && errors.Is(err, tt.Err) {
					// test OK
					return
				}
				t.Errorf("expect error %v, got %v", tt.Err, err)
			} else if tt.ExpectErr {
				t.Errorf("expect error %v, got <nil>", tt.Err)
			}
		})
	}

}

// newEchoServer setup a unix/pipe echo server for client to connect
func newEchoServer(name string, stopCh <-chan struct{}) (net.Addr, error) {
	l, err := newListener(fmt.Sprintf("%x", md5.Sum([]byte(name))))
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			fd, lerr := l.Accept()
			if lerr != nil {
				if strings.Contains(lerr.Error(), "closed") {
					return
				}
				continue
			}
			go echoServer(fd)
		}
	}()

	go func() {
		<-stopCh
		l.Close()
	}()

	return l.Addr(), nil
}

// newBlockServer setup a unix/pipe server but never accepts incoming connection
func newBlockServer(name string, stopCh <-chan struct{}) (net.Addr, error) {
	l, err := newListener(fmt.Sprintf("%x", md5.Sum([]byte(name))))
	if err != nil {
		return nil, err
	}

	go func() {
		<-stopCh
		l.Close()
	}()
	return l.Addr(), nil
}

func echoServer(c net.Conn) {
	defer c.Close()
	buf := make([]byte, 512)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		data := buf[0:n]
		_, err = c.Write(data)
		if err != nil {
			return
		}
	}
}
