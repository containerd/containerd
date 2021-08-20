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

package service

import (
	"context"

	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	ServiceName = "remotes-service"
)

// Config is the configuration for the remote service
type Config struct {
	DefaultPullService string `toml:"default_pull_service",json:"defaultPullService"`
	DefaultPushService string `toml:"default_push_service",json:"defaultPushService"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.RemotePlugin,
		ID:     ServiceName,
		Config: Config{DefaultPullService: plugin.RemoteDockerV1, DefaultPushService: plugin.RemoteDockerV1},
		Requires: []plugin.Type{
			plugin.RemotePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			plugins, err := ic.GetByType(plugin.RemotePlugin)
			if err != nil {
				return nil, err
			}

			cfg := ic.Config.(Config)

			s := &service{
				defaultPuller: cfg.DefaultPullService,
				pullers:       make(map[string]PullService),
				defaultPusher: cfg.DefaultPushService,
				pushers:       make(map[string]PushService),
			}

			for name, p := range plugins {
				i, err := p.Instance()
				if err != nil {
					continue
				}
				if pusher, ok := i.(PushService); ok {
					s.pushers[name] = pusher
				}
				if puller, ok := i.(PullService); ok {
					s.pullers[name] = puller
				}
			}

			return s, nil
		},
	})
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "remotes",
		Requires: []plugin.Type{
			plugin.RemotePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			s, err := ic.GetByID(plugin.RemotePlugin, ServiceName)
			if err != nil {
				return nil, err
			}
			return &server{s.(pushPullService)}, nil
		},
	})
}

type pushPullService interface {
	PullService
	PushService
}

// TODO: This whole thing may not make sense.
// It is here so you can select a remote to use when pulling or pushing.
// I'm not sure it is practical over just using the first registered remote.
// It also means we'd need some way to specify the remote on the pull/push request.
type service struct {
	pushers       map[string]PushService
	defaultPusher string
	pullers       map[string]PullService
	defaultPuller string
}

type remoteCtxKey struct{}

const mdRemoteKey = "io.containerd.remote"

// WithRemote is a helper function to set the remote on the context.
func WithRemote(ctx context.Context, r string) context.Context {
	return context.WithValue(ctx, remoteCtxKey{}, r)
}

func (s *service) Pull(ctx context.Context, r *api.PullRequest) (<-chan *PullResponseEnvelope, error) {
	remote := s.defaultPuller
	if k := ctx.Value(remoteCtxKey{}); k != nil {
		remote = k.(string)
	}
	puller, ok := s.pullers[remote]
	if !ok {
		return nil, errdefs.ErrUnavailable
	}
	return puller.Pull(ctx, r)
}

func (s *service) Push(ctx context.Context, r *api.PushRequest) (<-chan *PushResponseEnvelope, error) {
	remote := s.defaultPuller
	if k := ctx.Value(remoteCtxKey{}); k != nil {
		remote = k.(string)
	}

	pusher, ok := s.pushers[remote]
	if !ok {
		return nil, errdefs.ErrUnavailable
	}
	return pusher.Push(ctx, r)
}

type server struct {
	service pushPullService
}

func (s *server) Register(srv *grpc.Server) error {
	api.RegisterPushServiceServer(srv, s)
	api.RegisterPullServiceServer(srv, s)
	return nil
}

func (s *server) Push(req *api.PushRequest, srv api.PushService_PushServer) (retErr error) {
	ctx := srv.Context()

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if remotes := md.Get(mdRemoteKey); len(remotes) > 0 {
			ctx = WithRemote(ctx, remotes[0])
		}
	}

	ch, err := s.service.Push(ctx, req)
	if err != nil {
		return errdefs.ToGRPC(err)
	}

	for status := range ch {
		if err := srv.Send(status.PushResponse); err != nil {
			return err
		}
		if status.Err != nil {
			return errdefs.ToGRPC(status.Err)
		}
	}

	return nil
}

func (s *server) Pull(req *api.PullRequest, srv api.PullService_PullServer) (retErr error) {
	ctx := srv.Context()

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if remotes := md.Get(mdRemoteKey); len(remotes) > 0 {
			ctx = WithRemote(ctx, remotes[0])
		}
	}

	ch, err := s.service.Pull(ctx, req)
	if err != nil {
		return errdefs.ToGRPC(err)
	}

	for status := range ch {
		if err := srv.Send(status.PullResponse); err != nil {
			return err
		}
		if status.Err != nil {
			return errdefs.ToGRPC(status.Err)
		}
	}

	return nil
}
