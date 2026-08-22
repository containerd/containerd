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

package selinux

import (
	"context"

	api "github.com/containerd/containerd/api/services/selinux/v1"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/opencontainers/selinux/go-selinux/label"
	"google.golang.org/grpc"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.GRPCPlugin,
		ID:   "selinux",
		Requires: []plugin.Type{
			plugins.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			return &service{}, nil
		},
	})
}

type service struct {
	api.UnimplementedSelinuxServer
}

var _ api.SelinuxServer = &service{}

func (s *service) Register(gs *grpc.Server) error {
	api.RegisterSelinuxServer(gs, s)
	return nil
}

func (s *service) InitLabel(ctx context.Context, ir *api.InitLabelRequest) (*api.InitLabelResponse, error) {
	processLabel, mountLabel, err := label.InitLabels(ir.GetSelinuxOption())
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &api.InitLabelResponse{
		ProcessLabel: processLabel,
		MountLabel:   mountLabel,
	}, nil

}

func (s *service) ReleaseLabel(ctx context.Context, rr *api.ReleaseLabelRequest) (*api.ReleaseLabelResponse, error) {
	selinux.ReleaseLabel(rr.GetSelinuxLabel())
	return &api.ReleaseLabelResponse{}, nil
}
