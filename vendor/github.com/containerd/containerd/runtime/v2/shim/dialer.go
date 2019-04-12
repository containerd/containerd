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

package shim

import (
	"net"
	"sync"

	v1 "github.com/containerd/containerd/api/services/ttrpc/events/v1"
	"github.com/containerd/ttrpc"
	"github.com/pkg/errors"
)

type dialConnect func() (net.Conn, error)

var errDialerClosed = errors.New("events dialer is closed")

func newDialier(newFn dialConnect) *dialer {
	return &dialer{
		newFn: newFn,
	}
}

type dialer struct {
	mu sync.Mutex

	newFn   dialConnect
	service v1.EventsService
	conn    net.Conn
	closed  bool
}

func (d *dialer) Get() (v1.EventsService, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil, errDialerClosed
	}
	if d.service == nil {
		conn, err := d.newFn()
		if err != nil {
			return nil, err
		}
		d.conn = conn
		d.service = v1.NewEventsClient(ttrpc.NewClient(conn))
	}
	return d.service, nil
}

func (d *dialer) Put(err error) {
	if err != nil {
		d.mu.Lock()
		d.conn.Close()
		d.service = nil
		d.mu.Unlock()
	}
}

func (d *dialer) Close() (err error) {
	d.mu.Lock()
	if d.closed {
		return errDialerClosed
	}
	if d.conn != nil {
		err = d.conn.Close()
	}
	d.service = nil
	d.closed = true
	d.mu.Unlock()

	return err
}
