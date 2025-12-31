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

package metrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/docker/go-metrics"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/server/internal"
)

type config struct {
	Address string `toml:"address"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.ServerPlugin,
		ID:   "metrics",
		Requires: []plugin.Type{
			plugins.HTTPHandler,
		},
		Config: &config{},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			c := ic.Config.(*config)
			if c.Address == "" {
				return nil, plugin.ErrSkipPlugin
			}

			m := http.NewServeMux()
			m.Handle("/v1/metrics", metrics.Handler())

			return server{
				handler: m,
				address: c.Address,
			}, nil
		},
	})
}

type server struct {
	handler http.Handler
	address string
}

func (s server) Start(ctx context.Context) error {
	l, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to get listener for metrics endpoint: %w", err)
	}
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Minute, // "G112: Potential Slowloris Attack (gosec)"; not a real concern for our use, so setting a long timeout.
	}
	internal.Serve(ctx, l, srv.Serve)

	return nil
}
