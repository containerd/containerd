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

	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/remotes/docker/config"
	"github.com/containerd/containerd/remotes/service"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/semaphore"
)

const (
	dockerPusherPlugin = "docker-pusher-service"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.ServicePlugin,
		ID:   dockerPusherPlugin,
		// TODO: Don't hardcode /etc/containerd... but I didn't see anywhere that this was being set otherwise.
		Config: &Config{ConfigPath: "/etc/containerd/certs.d"},
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			mdb := m.(*metadata.DB)
			cfg := ic.Config.(*Config)

			return &localPusher{
				cs:         mdb.ContentStore(),
				configRoot: cfg.ConfigPath,
				headers:    cfg.Headers,
				lm:         metadata.NewLeaseManager(mdb),
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
	cs         content.Store
	configRoot string
	headers    http.Header
	lm         leases.Manager
}

func (l *localPusher) Push(ctx context.Context, req *api.PushRequest) (_ <-chan *service.PushResponseEnvelope, retErr error) {
	ctx, done, err := withLease(ctx, l.lm)
	defer func() {
		if retErr != nil {
			done(context.Background())
		}
	}()

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

	pusher, err := resolver.Pusher(ctx, req.Target)
	if err != nil {
		return nil, err
	}

	jobs := newJobTracker(tracker)

	wrap := func(h images.Handler) images.Handler {
		return images.Handlers(images.HandlerFunc(func(ctx context.Context, desc v1.Descriptor) ([]v1.Descriptor, error) {
			jobs.add(remotes.MakeRefKey(ctx, desc))
			return nil, nil
		}), h)
	}

	desc := descriptorAPIToOCI(req.Source)

	var matcher platforms.MatchComparer
	if req.Platform != nil {
		p := platformAPIToOCI(*req.Platform)
		matcher = platforms.Any(p)

		// If the platform is specified we need to get down to the manifest for
		// that platform, in cases where the provided descriptor is an index, otherwise
		// the pusher will try to push the index.
		if manifests, err := images.Children(ctx, l.cs, desc); err == nil && len(manifests) > 0 {
			matcher := platforms.NewMatcher(p)
			for _, manifest := range manifests {
				if manifest.Platform != nil && matcher.Match(*manifest.Platform) {
					if _, err := images.Children(ctx, l.cs, manifest); err != nil {
						return nil, errors.Wrap(err, "no matching manifest")
					}
					desc = manifest
					break
				}
			}
		}
	} else {
		matcher = platforms.All
	}

	var limit *semaphore.Weighted
	if req.MaxConcurrency > 0 {
		limit = semaphore.NewWeighted(req.MaxConcurrency)
	}

	ch := make(chan *service.PushResponseEnvelope)
	chErr := make(chan error, 1)

	// Be careful coordrinating these goroutines. The most important thing is
	// if PushContent returns an error that we can actually send the error. If
	// things are not setup correctly a cancelled context can prevent us from
	// sending errors down the channel.
	go func() {
		chErr <- remotes.PushContent(ctx, pusher, desc, l.cs, limit, matcher, wrap)
	}()

	go func() {
		defer done(context.Background())
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
