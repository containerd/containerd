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

package plugin

import (
	"context"
	"net/http"

	"github.com/containerd/containerd"
	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/remotes/docker/config"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.RemotePlugin,
		ID:   plugin.RemoteDockerV1,
		// TODO: Don't hardcode /etc/containerd... but I didn't see anywhere that this was being set otherwise.
		Config: &Config{ConfigPath: "/etc/containerd/certs.d"},
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			cfg := ic.Config.(*Config)

			opts, err := getServicesOpts(ic)
			if err != nil {
				return nil, err
			}
			client, err := containerd.New("", containerd.WithServices(opts...))
			if err != nil {
				return nil, err
			}
			return &remote{
				configRoot: cfg.ConfigPath,
				headers:    cfg.Headers,
				client:     client,
			}, nil
		},
	})
}

// Config is used to configure the docker-pusher
type Config struct {
	// ConfigPath sets the root path for registry host configuration (e.g. /etc/containerd/certs.d).
	ConfigPath string `toml:"config-path", json:"configPath"`
	// Headers are the HTTP headers that get set on push.
	Headers http.Header `toml:"headers", json:"headers"`
}

type remote struct {
	configRoot string
	headers    map[string][]string

	client *containerd.Client
}

func (r *remote) newResolver(ctx context.Context, auth *api.UserPassAuth, tracker docker.StatusTracker) remotes.Resolver {
	hostOptions := config.HostOptions{
		HostDir: config.HostDirFromRoot(r.configRoot),
		Credentials: func(host string) (string, string, error) {
			if auth == nil {
				return "", "", nil
			}
			return auth.Username, auth.Password, nil
		},
	}

	return docker.NewResolver(docker.ResolverOptions{
		Tracker: tracker,
		Hosts:   config.ConfigureHosts(ctx, hostOptions),
		Headers: r.headers,
	})
}
