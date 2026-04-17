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

package debug

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"github.com/containerd/containerd/v2/pkg/sys"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/server/internal"

	// Also register the internal pprof plugin, this can be separated
	// if pprof moves out of internal
	_ "github.com/containerd/containerd/v2/internal/pprof"
)

type config struct {
	Address string `toml:"address"`
	UID     int    `toml:"uid"`
	GID     int    `toml:"gid"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.ServerPlugin,
		ID:   "debug",
		Requires: []plugin.Type{
			plugins.HTTPHandler,
		},
		Config: &config{},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			c := ic.Config.(*config)
			if c.Address == "" {
				return nil, plugin.ErrSkipPlugin
			}

			p, err := ic.GetByID(plugins.HTTPHandler, "pprof")
			if err != nil {
				if errors.Is(err, plugin.ErrPluginNotFound) {
					log.G(ic.Context).Warn("debug server is specified but pprof plugin not found, skipping debug server")
					return nil, plugin.ErrSkipPlugin
				}
				return nil, err
			}

			return server{
				handler: p.(*http.Server),
				config:  *c,
			}, nil
		},
	})
}

type server struct {
	handler *http.Server
	config  config
}

func (s server) Start(ctx context.Context) error {
	var (
		l   net.Listener
		err error
	)
	if internal.IsLocalAddress(s.config.Address) {
		if l, err = sys.GetLocalListener(s.config.Address, s.config.UID, s.config.GID); err != nil {
			return fmt.Errorf("failed to get listener for debug endpoint: %w", err)
		}
	} else {
		if l, err = net.Listen("tcp", s.config.Address); err != nil {
			return fmt.Errorf("failed to get listener for debug endpoint: %w", err)
		}
	}
	internal.Serve(ctx, l, s.handler.Serve)

	return nil
}
