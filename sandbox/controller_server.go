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

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/golang/protobuf/ptypes/empty"
)

type controllerServer struct {
	ctrl Controller
	api.UnimplementedControllerServer
}

func FromService(service Controller) api.ControllerServer {
	return &controllerServer{ctrl: service}
}

var _ api.ControllerServer = &controllerServer{}

func (p *controllerServer) Start(ctx context.Context, req *api.ControllerStartRequest) (*api.ControllerStartResponse, error) {
	in, err := FromProto(req.Sandbox)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	out, err := p.ctrl.Start(ctx, in)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	sb, err := ToProto(out)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.ControllerStartResponse{Sandbox: sb}, nil
}

func (p *controllerServer) Shutdown(ctx context.Context, req *api.ControllerShutdownRequest) (*empty.Empty, error) {
	err := p.ctrl.Shutdown(ctx, req.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &empty.Empty{}, nil
}

func (p *controllerServer) Pause(ctx context.Context, req *api.ControllerPauseRequest) (*empty.Empty, error) {
	err := p.ctrl.Shutdown(ctx, req.SandboxID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &empty.Empty{}, nil
}

func (p *controllerServer) Resume(ctx context.Context, req *api.ControllerResumeRequest) (*empty.Empty, error) {
	err := p.ctrl.Resume(ctx, req.SandboxID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &empty.Empty{}, nil
}

func (p *controllerServer) Update(ctx context.Context, req *api.ControllerUpdateRequest) (*api.ControllerUpdateResponse, error) {
	in, err := FromProto(req.Sandbox)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	out, err := p.ctrl.Start(ctx, in)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	sb, err := ToProto(out)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.ControllerUpdateResponse{Sandbox: sb}, nil
}

func (p *controllerServer) AppendContainer(ctx context.Context, req *api.ControllerAppendContainerRequest) (*api.ControllerAppendContainerResponse, error) {
	in, err := ContainerFromProto(req.Container)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	out, err := p.ctrl.AppendContainer(ctx, req.SandboxID, &in)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	container, err := ContainerToProto(*out)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.ControllerAppendContainerResponse{
		Container: container,
	}, nil
}

func (p *controllerServer) UpdateContainer(ctx context.Context, req *api.ControllerUpdateContainerRequest) (*api.ControllerUpdateContainerResponse, error) {
	in, err := ContainerFromProto(req.Container)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	out, err := p.ctrl.UpdateContainer(ctx, req.SandboxID, &in)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	container, err := ContainerToProto(*out)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &api.ControllerUpdateContainerResponse{
		Container: container,
	}, nil
}

func (p *controllerServer) RemoveContainer(ctx context.Context, req *api.ControllerRemoveContainerRequest) (*empty.Empty, error) {
	err := p.ctrl.RemoveContainer(ctx, req.SandboxID, req.ContainerID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &empty.Empty{}, nil
}

func (p *controllerServer) Ping(ctx context.Context, req *api.ControllerPingRequest) (*empty.Empty, error) {
	err := p.ctrl.Ping(ctx, req.SandboxID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &empty.Empty{}, nil
}

func (p *controllerServer) Status(ctx context.Context, req *api.ControllerStatusRequest) (*api.ControllerStatusResponse, error) {
	status, err := p.ctrl.Status(ctx, req.ID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	proto := StatusToProto(status)
	return &api.ControllerStatusResponse{Status: proto}, nil
}
