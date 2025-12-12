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

package ttrpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/sys"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/server/internal"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/containerd/ttrpc"
)

type config struct {
	Address        string `toml:"address"`
	UID            int    `toml:"uid"`
	GID            int    `toml:"gid"`
	MaxRecvMsgSize int    `toml:"max_recv_message_size"`
	MaxSendMsgSize int    `toml:"max_send_message_size"`
}

func (c *config) GetAddress() string {
	return c.Address
}

func (c *config) SetAddress(s string) {
	c.Address = s
}

func init() {
	// Keep the default the same for compatibility
	defaultAddress := defaults.DefaultAddress + ".ttrpc"
	registry.Register(&plugin.Registration{
		Type: plugins.ServerPlugin,
		ID:   "ttrpc",
		Requires: []plugin.Type{
			plugins.TTRPCPlugin,
			plugins.GRPCPlugin,
		},
		Config: &config{
			Address:        defaultAddress,
			UID:            os.Geteuid(),
			GID:            os.Getegid(),
			MaxRecvMsgSize: defaults.DefaultMaxRecvMsgSize,
			MaxSendMsgSize: defaults.DefaultMaxSendMsgSize,
		},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			c := ic.Config.(*config)
			if c.Address == "" {
				return nil, plugin.ErrSkipPlugin
			}

			s, err := newTTRPCServer()
			if err != nil {
				return nil, err
			}
			// ttrpcService allows TTRPC services to be registered with the underlying server
			type ttrpcService interface {
				RegisterTTRPC(*ttrpc.Server) error
			}
			var hasService bool
			ps, err := ic.GetByType(plugins.TTRPCPlugin) // ensure grpc plugin is initialized
			if err != nil && !errors.Is(err, plugin.ErrPluginNotFound) {
				return nil, err
			}
			for _, p := range ps {
				if gs, isGRPC := p.(ttrpcService); isGRPC {
					if err := gs.RegisterTTRPC(s); err != nil {
						return nil, fmt.Errorf("failed to register ttrpc service: %w", err)
					}
					hasService = true
				}
			}
			ps, err = ic.GetByType(plugins.GRPCPlugin) // ensure grpc plugin is initialized
			if err != nil && !errors.Is(err, plugin.ErrPluginNotFound) {
				return nil, err
			}
			for _, p := range ps {
				if gs, isGRPC := p.(ttrpcService); isGRPC {
					if err := gs.RegisterTTRPC(s); err != nil {
						return nil, fmt.Errorf("failed to register ttrpc service: %w", err)
					}
					hasService = true
				}
			}
			if !hasService {
				return nil, fmt.Errorf("no ttrpc services configured: %w", plugin.ErrSkipPlugin)
			}
			return server{
				Server: s,
				config: *c,
			}, nil
		},
	})
}

type server struct {
	*ttrpc.Server
	config config
}

func (s server) Start(ctx context.Context) error {
	// setup the ttrpc endpoint
	tl, err := sys.GetLocalListener(s.config.Address, s.config.UID, s.config.GID)
	if err != nil {
		return fmt.Errorf("failed to get listener for main ttrpc endpoint: %w", err)
	}

	internal.Serve(ctx, tl, func(l net.Listener) error {
		return s.Serve(context.WithoutCancel(ctx), l)
	})

	return nil
}
