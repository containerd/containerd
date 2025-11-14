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

package mounts

import (
	"context"

	api "github.com/containerd/containerd/api/services/mounts/v1"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/mount/proxy"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/plugins"
)

var empty = &ptypes.Empty{}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.GRPCPlugin,
		ID:   "mounts",
		Requires: []plugin.Type{
			plugins.MountManagerPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			i, err := ic.GetSingle(plugins.MountManagerPlugin)
			if err != nil {
				return nil, err
			}
			return &service{mm: i.(mount.Manager)}, nil
		},
	})
}

type service struct {
	mm mount.Manager
	api.UnimplementedMountsServer
}

func (s *service) Register(server *grpc.Server) error {
	api.RegisterMountsServer(server, s)
	return nil
}

func (s *service) Activate(ctx context.Context, in *api.ActivateRequest) (*api.ActivateResponse, error) {
	log.G(ctx).WithFields(log.Fields{"name": in.Name, "temp": in.Temporary, "mounts": len(in.Mounts)}).Debug("activate mounts")
	var opts []mount.ActivateOpt
	if in.Temporary {
		opts = append(opts, mount.WithTemporary)
	}
	if in.Labels != nil {
		opts = append(opts, mount.WithLabels(in.Labels))
	}

	info, err := s.mm.Activate(ctx, in.Name, mount.FromProto(in.Mounts), opts...)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &api.ActivateResponse{
		Info: proxy.ActivationInfoToProto(info),
	}, nil

}

func (s *service) Deactivate(ctx context.Context, in *api.DeactivateRequest) (*emptypb.Empty, error) {
	log.G(ctx).WithField("name", in.Name).Debug("deactivate mounts")
	if err := s.mm.Deactivate(ctx, in.Name); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return empty, nil
}

func (s *service) Info(ctx context.Context, in *api.InfoRequest) (*api.InfoResponse, error) {
	log.G(ctx).WithField("name", in.Name).Debug("mount info")
	info, err := s.mm.Info(ctx, in.Name)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &api.InfoResponse{
		Info: proxy.ActivationInfoToProto(info),
	}, nil
}

func (s *service) Update(ctx context.Context, in *api.UpdateRequest) (*api.UpdateResponse, error) {
	info, err := s.mm.Update(ctx, proxy.ActivationInfoFromProto(in.Info), in.UpdateMask.GetPaths()...)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &api.UpdateResponse{
		Info: proxy.ActivationInfoToProto(info),
	}, nil
}

func (s *service) List(in *api.ListRequest, ms api.Mounts_ListServer) error {
	log.G(ms.Context()).Debug("list mounts")
	infos, err := s.mm.List(ms.Context(), in.Filters...)
	if err != nil {
		return errgrpc.ToGRPC(err)
	}
	for _, info := range infos {
		err = ms.Send(&api.ListMessage{
			Info: proxy.ActivationInfoToProto(info),
		})
		if err != nil {
			return errgrpc.ToGRPC(err)
		}
	}

	return nil
}
