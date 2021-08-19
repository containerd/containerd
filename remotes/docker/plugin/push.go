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
	"strings"
	"time"

	"github.com/containerd/containerd"
	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/remotes/docker/config"
	"github.com/containerd/containerd/remotes/service"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	dockerPusherPlugin = "docker-pusher-service"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.RemotePlugin,
		ID:   dockerPusherPlugin,
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
			return &localPusher{
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
	ConfigPath string `toml:"config-path",json:"configPath"`
	// Headers are the HTTP headers that get set on push.
	Headers http.Header `toml:"headers",json:"headers"`
}

type localPusher struct {
	client     *containerd.Client
	configRoot string
	headers    http.Header
}

func (l *localPusher) Push(ctx context.Context, req *api.PushRequest) (_ <-chan *service.PushResponseEnvelope, retErr error) {
	tracker := docker.NewInMemoryTracker()

	log.G(ctx).WithField("digest", req.Source).Debug("push request received")

	auth := req.Auth
	hostOptions := config.HostOptions{
		HostDir: config.HostDirFromRoot(l.configRoot),
		Credentials: func(host string) (string, string, error) {
			return auth.Username, auth.Password, nil
		},
	}

	resolver := docker.NewResolver(docker.ResolverOptions{
		Tracker: tracker,
		Hosts:   config.ConfigureHosts(ctx, hostOptions),
		Headers: l.headers,
	})

	if !strings.Contains(req.Target, "@") {
		req.Target = req.Target + "@" + req.Source.Digest.String()
	}

	jobs := newJobTracker(tracker)

	ch := make(chan *service.PushResponseEnvelope)
	chErr := make(chan error, 1)

	// Be careful coordrinating these goroutines. The most important thing is
	// if PushContent returns an error that we can actually send the error. If
	// things are not setup correctly a cancelled context can prevent us from
	// sending errors down the channel.
	go func() {
		chErr <- l.client.Push(ctx,
			req.Target,
			descriptorAPIToOCI(req.Source),
			containerd.WithResolver(resolver),
			containerd.WithImageHandlerWrapper(func(h images.Handler) images.Handler {
				return images.Handlers(h, images.HandlerFunc(func(ctx context.Context, desc v1.Descriptor) (subdescs []v1.Descriptor, err error) {
					jobs.add(remotes.MakeRefKey(ctx, desc))
					return nil, nil
				}))
			}),
			func(_ *containerd.Client, rc *containerd.RemoteContext) error {
				if req.Platform != nil {
					p := platformAPIToOCI(*req.Platform)
					rc.PlatformMatcher = platforms.Any(p)
				} else {
					rc.PlatformMatcher = platforms.All
				}

				if req.MaxConcurrency > 0 {
					rc.MaxConcurrentUploadedLayers = int(req.MaxConcurrency)
				}
				return nil
			},
		)

	}()

	go func() {
		var ticker = time.NewTicker(100 * time.Millisecond)

		defer close(ch)
		defer ticker.Stop()

		var (
			pushErr error
			done    bool
		)
		for {
			select {
			case <-ticker.C:
			case pushErr = <-chErr:
				done = true
			case <-ctx.Done():
				done = true
			}

			select {
			case ch <- &service.PushResponseEnvelope{Err: pushErr, PushResponse: &api.PushResponse{Statuses: jobs.status()}}:
				if done {
					return
				}
			case <-ctx.Done():
				timer := time.NewTimer(5 * time.Second)
				select {
				case ch <- &service.PushResponseEnvelope{Err: ctx.Err(), PushResponse: &api.PushResponse{Statuses: jobs.status()}}:
				case <-timer.C:
				}
				timer.Stop()
				return
			}
		}
	}()

	return ch, nil

}
