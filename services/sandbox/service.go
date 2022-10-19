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

package sandbox

import (
	"context"
	"errors"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/containerd/services"
	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "sandboxes",
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			plugins, err := ic.GetByType(plugin.ServicePlugin)
			if err != nil {
				return nil, err
			}
			p, ok := plugins[services.SandboxService]
			if !ok {
				return nil, errors.New("sandbox service not found")
			}
			i, err := p.Instance()
			if err != nil {
				return nil, err
			}
			ss := i.(map[string]sandbox.Sandboxer)
			return newService(ss), nil
		},
	})
}

type service struct {
	ss map[string]sandbox.Sandboxer
	api.UnsafeSandboxerServer
}

func (s *service) getSandboxer(name string) (sandbox.Sandboxer, error) {
	if name == "" {
		return nil, errdefs.ToGRPCf(errdefs.ErrInvalidArgument, "sandboxer argument missing")
	}

	sn := s.ss[name]
	if sn == nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrInvalidArgument, "snapshotter not loaded: %s", name)
	}
	return sn, nil
}

func (s *service) Create(ctx context.Context, req *api.CreateSandboxRequest) (*api.CreateSandboxResponse, error) {
	log.G(ctx).WithField("sandbox", req.Sandbox).Debugf("create sandbox")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	sb, err := sandbox.FromProto(req.Sandbox)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	newSb, err := sandboxer.Create(ctx, sb)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	proto, err := sandbox.ToProto(newSb)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.CreateSandboxResponse{
		Sandbox: proto,
	}, nil
}

func (s *service) Update(ctx context.Context, req *api.UpdateSandboxRequest) (*api.UpdateSandboxResponse, error) {
	log.G(ctx).WithField("sandbox", req.Sandbox).Debugf("update sandbox")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	sb, err := sandbox.FromProto(req.Sandbox)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	newSb, err := sandboxer.Update(ctx, sb)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	proto, err := sandbox.ToProto(newSb)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.UpdateSandboxResponse{
		Sandbox: proto,
	}, nil
}

func (s *service) AppendContainer(ctx context.Context, req *api.AppendContainerRequest) (*api.AppendContainerResponse, error) {
	log.G(ctx).WithField("sandbox-id", req.SandboxID).WithField("container", req.Container).Debugf("append container")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	cont, err := sandbox.ContainerFromProto(req.Container)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	newCont, err := sandboxer.AppendContainer(ctx, req.SandboxID, &cont)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	proto, err := sandbox.ContainerToProto(*newCont)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.AppendContainerResponse{
		Container: proto,
	}, nil
}

func (s *service) UpdateContainer(ctx context.Context, req *api.UpdateContainerRequest) (*api.UpdateContainerResponse, error) {
	log.G(ctx).WithField("sandbox-id", req.SandboxID).WithField("container", req.Container).Debugf("update container")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	cont, err := sandbox.ContainerFromProto(req.Container)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	newCont, err := sandboxer.UpdateContainer(ctx, req.SandboxID, &cont)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	proto, err := sandbox.ContainerToProto(*newCont)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.UpdateContainerResponse{
		Container: proto,
	}, nil
}

func (s *service) RemoveContainer(ctx context.Context, req *api.RemoveContainerRequest) (*empty.Empty, error) {
	log.G(ctx).WithField("sandbox-id", req.SandboxID).WithField("container-id", req.ContainerID).Debugf("remove container")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	err = sandboxer.RemoveContainer(ctx, req.SandboxID, req.ContainerID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &empty.Empty{}, nil
}

func (s *service) Delete(ctx context.Context, req *api.DeleteSandboxRequest) (*empty.Empty, error) {
	log.G(ctx).WithField("sandbox-id", req.ID).Debugf("delete sandbox")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	err = sandboxer.Delete(ctx, req.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &empty.Empty{}, nil
}

func (s *service) List(ctx context.Context, req *api.ListSandboxRequest) (*api.ListSandboxResponse, error) {
	log.G(ctx).WithField("filters", req.Filters).Debugf("list sandbox")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	sbs, err := sandboxer.List(ctx, req.Filters...)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var ret []*api.Sandbox
	for i := range sbs {
		newSb, err := sandbox.ToProto(&sbs[i])
		if err != nil {
			return nil, errdefs.ToGRPC(err)
		}
		ret = append(ret, newSb)
	}
	return &api.ListSandboxResponse{
		List: ret,
	}, nil
}

func (s *service) Get(ctx context.Context, req *api.GetSandboxRequest) (*api.GetSandboxResponse, error) {
	log.G(ctx).WithField("id", req.ID).Debugf("get sandbox")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	sb, err := sandboxer.Get(ctx, req.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	newSb, err := sandbox.ToProto(sb)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.GetSandboxResponse{
		Sandbox: newSb,
	}, nil
}

func (s *service) Status(ctx context.Context, req *api.StatusRequest) (*api.StatusResponse, error) {
	log.G(ctx).WithField("id", req.ID).Debugf("status sandbox")
	sandboxer, err := s.getSandboxer(req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	status, err := sandboxer.Status(ctx, req.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	proto := sandbox.StatusToProto(status)

	return &api.StatusResponse{
		Status: proto,
	}, nil
}

var _ api.SandboxerServer = &service{}

func newService(ss map[string]sandbox.Sandboxer) *service {
	srv := &service{
		ss: ss,
	}

	return srv
}

func (s *service) Register(srv *grpc.Server) error {
	api.RegisterSandboxerServer(srv, s)
	return nil
}
