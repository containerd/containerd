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

package client

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/containerd/ttrpc"

	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime/shim"
	shimapi "github.com/containerd/containerd/runtime/shim/v1"
	ptypes "github.com/gogo/protobuf/types"
)

var empty = &ptypes.Empty{}

// Opt is an option for a shim client configuration
type Opt func(context.Context, shim.Config) (shimapi.ShimService, io.Closer, error)

func connect(address string, d func(string, time.Duration) (net.Conn, error)) (net.Conn, error) {
	return d(address, 100*time.Second)
}

// WithConnect connects to an existing shim
func WithConnect(address string, onClose func()) Opt {
	return func(ctx context.Context, config shim.Config) (shimapi.ShimService, io.Closer, error) {
		conn, err := connect(address, annonDialer)
		if err != nil {
			return nil, nil, err
		}
		client := ttrpc.NewClient(conn)
		client.OnClose(onClose)
		return shimapi.NewShimClient(client), conn, nil
	}
}

// WithLocal uses an in process shim
func WithLocal(publisher events.Publisher) func(context.Context, shim.Config) (shimapi.ShimService, io.Closer, error) {
	return func(ctx context.Context, config shim.Config) (shimapi.ShimService, io.Closer, error) {
		service, err := shim.NewService(config, publisher)
		if err != nil {
			return nil, nil, err
		}
		return shim.NewLocal(service), nil, nil
	}
}

// New returns a new shim client
func New(ctx context.Context, config shim.Config, opt Opt) (*Client, error) {
	s, c, err := opt(ctx, config)
	if err != nil {
		return nil, err
	}
	return &Client{
		ShimService: s,
		c:           c,
		exitCh:      make(chan struct{}),
	}, nil
}

// Client is a shim client containing the connection to a shim
type Client struct {
	shimapi.ShimService

	c        io.Closer
	exitCh   chan struct{}
	exitOnce sync.Once
}

// IsAlive returns true if the shim can be contacted.
// NOTE: a negative answer doesn't mean that the process is gone.
func (c *Client) IsAlive(ctx context.Context) (bool, error) {
	_, err := c.ShimInfo(ctx, empty)
	if err != nil {
		// TODO(stevvooe): There are some error conditions that need to be
		// handle with unix sockets existence to give the right answer here.
		return false, err
	}
	return true, nil
}

// Close the cient connection
func (c *Client) Close() error {
	if c.c == nil {
		return nil
	}
	return c.c.Close()
}
