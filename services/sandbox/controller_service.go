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
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"

	eventtypes "github.com/containerd/containerd/v2/api/events"
	api "github.com/containerd/containerd/v2/api/services/sandbox/v1"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/events"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/sandbox"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.GRPCPlugin,
		ID:   "sandbox-controllers",
		Requires: []plugin.Type{
			plugins.SandboxControllerPlugin,
			plugins.EventPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			sandboxers, err := ic.GetByType(plugins.SandboxControllerPlugin)
			if err != nil {
				return nil, err
			}

			sc := make(map[string]sandbox.Controller)
			for name, p := range sandboxers {
				sc[name] = p.(sandbox.Controller)
			}

			ep, err := ic.GetSingle(plugins.EventPlugin)
			if err != nil {
				return nil, err
			}

			return &controllerService{
				sc:        sc,
				publisher: ep.(events.Publisher),
			}, nil
		},
	})
}

type controllerService struct {
	sc        map[string]sandbox.Controller
	publisher events.Publisher
	api.UnimplementedControllerServer
}

var _ api.ControllerServer = (*controllerService)(nil)

func (s *controllerService) Register(server *grpc.Server) error {
	api.RegisterControllerServer(server, s)
	return nil
}

func (s *controllerService) getController(name string) (sandbox.Controller, error) {
	if len(name) == 0 {
		return nil, fmt.Errorf("%w: sandbox controller name can not be empty", errdefs.ErrInvalidArgument)
	}
	if ctrl, ok := s.sc[name]; ok {
		return ctrl, nil
	}
	return nil, fmt.Errorf("%w: failed to get sandbox controller by %s", errdefs.ErrNotFound, name)
}

func (s *controllerService) Create(ctx context.Context, req *api.ControllerCreateRequest) (*api.ControllerCreateResponse, error) {
	log.G(ctx).WithField("req", req).Debug("create sandbox")
	// TODO: Rootfs
	ctrl, err := s.getController(req.Sandboxer)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	err = ctrl.Create(ctx, sandbox.Sandbox{ID: req.GetSandboxID()}, sandbox.WithOptions(req.GetOptions()))
	if err != nil {
		return &api.ControllerCreateResponse{}, errdefs.ToGRPC(err)
	}

	if err := s.publisher.Publish(ctx, "sandboxes/create", &eventtypes.SandboxCreate{
		SandboxID: req.GetSandboxID(),
	}); err != nil {
		return &api.ControllerCreateResponse{}, errdefs.ToGRPC(err)
	}

	return &api.ControllerCreateResponse{
		SandboxID: req.GetSandboxID(),
	}, nil
}

func (s *controllerService) Start(ctx context.Context, req *api.ControllerStartRequest) (*api.ControllerStartResponse, error) {
	log.G(ctx).WithField("req", req).Debug("start sandbox")
	ctrl, err := s.getController(req.Sandboxer)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	inst, err := ctrl.Start(ctx, req.GetSandboxID())
	if err != nil {
		return &api.ControllerStartResponse{}, errdefs.ToGRPC(err)
	}

	if err := s.publisher.Publish(ctx, "sandboxes/start", &eventtypes.SandboxStart{
		SandboxID: req.GetSandboxID(),
	}); err != nil {
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
	ctrl, err := s.getController(req.Sandboxer)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.ControllerStopResponse{}, errdefs.ToGRPC(ctrl.Stop(ctx, req.GetSandboxID(), sandbox.WithTimeout(time.Duration(req.TimeoutSecs)*time.Second)))
}

func (s *controllerService) Wait(ctx context.Context, req *api.ControllerWaitRequest) (*api.ControllerWaitResponse, error) {
	log.G(ctx).WithField("req", req).Debug("wait sandbox")
	ctrl, err := s.getController(req.Sandboxer)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	exitStatus, err := ctrl.Wait(ctx, req.GetSandboxID())
	if err != nil {
		return &api.ControllerWaitResponse{}, errdefs.ToGRPC(err)
	}

	if err := s.publisher.Publish(ctx, "sandboxes/exit", &eventtypes.SandboxExit{
		SandboxID:  req.GetSandboxID(),
		ExitStatus: exitStatus.ExitStatus,
		ExitedAt:   protobuf.ToTimestamp(exitStatus.ExitedAt),
	}); err != nil {
		return &api.ControllerWaitResponse{}, errdefs.ToGRPC(err)
	}

	return &api.ControllerWaitResponse{
		ExitStatus: exitStatus.ExitStatus,
		ExitedAt:   protobuf.ToTimestamp(exitStatus.ExitedAt),
	}, nil
}

func (s *controllerService) Status(ctx context.Context, req *api.ControllerStatusRequest) (*api.ControllerStatusResponse, error) {
	log.G(ctx).WithField("req", req).Debug("sandbox status")
	ctrl, err := s.getController(req.Sandboxer)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	cstatus, err := ctrl.Status(ctx, req.GetSandboxID(), req.GetVerbose())
	if err != nil {
		return &api.ControllerStatusResponse{}, errdefs.ToGRPC(err)
	}
	extra := &anypb.Any{}
	if cstatus.Extra != nil {
		extra = &anypb.Any{
			TypeUrl: cstatus.Extra.GetTypeUrl(),
			Value:   cstatus.Extra.GetValue(),
		}
	}
	return &api.ControllerStatusResponse{
		SandboxID: cstatus.SandboxID,
		Pid:       cstatus.Pid,
		State:     cstatus.State,
		Info:      cstatus.Info,
		CreatedAt: protobuf.ToTimestamp(cstatus.CreatedAt),
		ExitedAt:  protobuf.ToTimestamp(cstatus.ExitedAt),
		Extra:     extra,
	}, nil
}

func (s *controllerService) Shutdown(ctx context.Context, req *api.ControllerShutdownRequest) (*api.ControllerShutdownResponse, error) {
	log.G(ctx).WithField("req", req).Debug("shutdown sandbox")
	ctrl, err := s.getController(req.Sandboxer)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &api.ControllerShutdownResponse{}, errdefs.ToGRPC(ctrl.Shutdown(ctx, req.GetSandboxID()))
}

func (s *controllerService) Metrics(ctx context.Context, req *api.ControllerMetricsRequest) (*api.ControllerMetricsResponse, error) {
	log.G(ctx).WithField("req", req).Debug("sandbox metrics")
	ctrl, err := s.getController(req.Sandboxer)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	metrics, err := ctrl.Metrics(ctx, req.GetSandboxID())
	if err != nil {
		return &api.ControllerMetricsResponse{}, errdefs.ToGRPC(err)
	}
	return &api.ControllerMetricsResponse{
		Metrics: metrics,
	}, nil
}
