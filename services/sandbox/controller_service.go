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
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/containerd/sandbox"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "sandbox-controllers",
		Requires: []plugin.Type{
			plugin.SandboxControllerPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			sc, err := ic.GetByID(plugin.SandboxControllerPlugin, "local")
			if err != nil {
				return nil, err
			}

			return &controllerService{
				local: sc.(sandbox.Controller),
			}, nil
		},
	})
}

type controllerService struct {
	local sandbox.Controller
	api.UnimplementedControllerServer
}

var _ api.ControllerServer = (*controllerService)(nil)

func (s *controllerService) Register(server *grpc.Server) error {
	api.RegisterControllerServer(server, s)
	return nil
}

func (s *controllerService) Create(ctx context.Context, req *api.ControllerCreateRequest) (*api.ControllerCreateResponse, error) {
	log.G(ctx).WithField("req", req).Debug("create sandbox")
	// TODO: Rootfs, any
	err := s.local.Create(ctx, req.GetSandboxID())
	if err != nil {
		return &api.ControllerCreateResponse{}, errdefs.ToGRPC(err)
	}
	return &api.ControllerCreateResponse{
		SandboxID: req.GetSandboxID(),
	}, nil
}

func (s *controllerService) Start(ctx context.Context, req *api.ControllerStartRequest) (*api.ControllerStartResponse, error) {
	log.G(ctx).WithField("req", req).Debug("start sandbox")
	inst, err := s.local.Start(ctx, req.GetSandboxID())
	if err != nil {
		return &api.ControllerStartResponse{}, errdefs.ToGRPC(err)
	}
	return &api.ControllerStartResponse{
		SandboxID: inst.SandboxID,
		Pid:       inst.Pid,
		CreatedAt: protobuf.ToTimestamp(inst.CreatedAt),
		Labels:    inst.Labels,
	}, nil
}

func (s *controllerService) Stop(ctx context.Context, req *api.ControllerStopRequest) (*api.ControllerStopResponse, error) {
	log.G(ctx).WithField("req", req).Debug("delete sandbox")
	return &api.ControllerStopResponse{}, errdefs.ToGRPC(s.local.Stop(ctx, req.GetSandboxID()))
}

func (s *controllerService) Wait(ctx context.Context, req *api.ControllerWaitRequest) (*api.ControllerWaitResponse, error) {
	log.G(ctx).WithField("req", req).Debug("wait sandbox")
	exitStatus, err := s.local.Wait(ctx, req.GetSandboxID())
	if err != nil {
		return &api.ControllerWaitResponse{}, errdefs.ToGRPC(err)
	}
	return &api.ControllerWaitResponse{
		ExitStatus: exitStatus.ExitStatus,
		ExitedAt:   protobuf.ToTimestamp(exitStatus.ExitedAt),
	}, nil
}

func (s *controllerService) Status(ctx context.Context, req *api.ControllerStatusRequest) (*api.ControllerStatusResponse, error) {
	log.G(ctx).WithField("req", req).Debug("sandbox status")
	cstatus, err := s.local.Status(ctx, req.GetSandboxID(), req.GetVerbose())
	if err != nil {
		return &api.ControllerStatusResponse{}, errdefs.ToGRPC(err)
	}
	return &api.ControllerStatusResponse{
		SandboxID: cstatus.SandboxID,
		Pid:       cstatus.Pid,
		State:     cstatus.State,
		Info:      cstatus.Info,
		CreatedAt: protobuf.ToTimestamp(cstatus.CreatedAt),
		ExitedAt:  protobuf.ToTimestamp(cstatus.ExitedAt),
		Extra: &anypb.Any{
			TypeUrl: cstatus.Extra.GetTypeUrl(),
			Value:   cstatus.Extra.GetValue(),
		},
	}, nil
}

func (s *controllerService) Shutdown(ctx context.Context, req *api.ControllerShutdownRequest) (*api.ControllerShutdownResponse, error) {
	log.G(ctx).WithField("req", req).Debug("shutdown sandbox")
	return &api.ControllerShutdownResponse{}, errdefs.ToGRPC(s.local.Shutdown(ctx, req.GetSandboxID()))
}
