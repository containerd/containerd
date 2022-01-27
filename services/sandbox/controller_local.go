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

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	v2 "github.com/containerd/containerd/runtime/v2"
	"github.com/containerd/containerd/runtime/v2/task"
	proto "github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/containerd/services"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.ServicePlugin,
		ID:   services.SandboxControllerService,
		Requires: []plugin.Type{
			plugin.RuntimePluginV2,
			plugin.MetadataPlugin,
			plugin.EventPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			shimPlugin, err := ic.GetByID(plugin.RuntimePluginV2, "shim")
			if err != nil {
				return nil, err
			}

			metadataPlugin, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			exchangePlugin, err := ic.GetByID(plugin.EventPlugin, "exchange")
			if err != nil {
				return nil, err
			}

			var (
				shims     = shimPlugin.(*v2.ShimManager)
				publisher = exchangePlugin.(*exchange.Exchange)
				db        = metadataPlugin.(*metadata.DB)
				store     = metadata.NewSandboxStore(db)
			)

			return &controllerLocal{
				shims:     shims,
				store:     store,
				publisher: publisher,
			}, nil
		},
	})
}

type controllerLocal struct {
	shims     *v2.ShimManager
	store     sandbox.Store
	publisher events.Publisher
}

var _ api.ControllerClient = (*controllerLocal)(nil)

func (c *controllerLocal) Start(ctx context.Context, in *api.ControllerStartRequest, opts ...grpc.CallOption) (*api.ControllerStartResponse, error) {
	if _, err := c.shims.Get(ctx, in.SandboxID); err == nil {
		return nil, fmt.Errorf("sandbox %s already running: %w", in.SandboxID, errdefs.ErrAlreadyExists)
	}

	info, err := c.store.Get(ctx, in.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to query sandbox metadata from store: %w", err)
	}

	shim, err := c.shims.Start(ctx, in.SandboxID, runtime.CreateOpts{
		Spec:           info.Spec,
		RuntimeOptions: info.Runtime.Options,
		Runtime:        info.Runtime.Name,
		TaskOptions:    nil,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to start new sandbox: %w", err)
	}

	svc := task.NewSandboxClient(shim.Client())

	_, err = svc.StartSandbox(ctx, &proto.StartSandboxRequest{
		SandboxID: in.SandboxID,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to start sandbox %s: %w", in.SandboxID, err)
	}

	return &api.ControllerStartResponse{}, nil
}

func (c *controllerLocal) Shutdown(ctx context.Context, in *api.ControllerShutdownRequest, opts ...grpc.CallOption) (*api.ControllerShutdownResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	if _, err := svc.StopSandbox(ctx, &proto.StopSandboxRequest{
		SandboxID:   in.SandboxID,
		TimeoutSecs: 0,
	}); err != nil {
		return nil, fmt.Errorf("failed to stop sandbox: %w", err)
	}

	if err := c.shims.Delete(ctx, in.SandboxID); err != nil {
		return nil, fmt.Errorf("failed to delete sandbox shim: %w", err)
	}

	return &api.ControllerShutdownResponse{}, nil
}

func (c *controllerLocal) Pause(ctx context.Context, in *api.ControllerPauseRequest, opts ...grpc.CallOption) (*api.ControllerPauseResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	if _, err := svc.PauseSandbox(ctx, &proto.PauseSandboxRequest{
		SandboxID: in.SandboxID,
	}); err != nil {
		return nil, errors.Wrapf(err, "failed to resume sandbox %s", in.SandboxID)
	}

	return &api.ControllerPauseResponse{}, nil
}

func (c *controllerLocal) Resume(ctx context.Context, in *api.ControllerResumeRequest, opts ...grpc.CallOption) (*api.ControllerResumeResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	if _, err := svc.ResumeSandbox(ctx, &proto.ResumeSandboxRequest{
		SandboxID: in.SandboxID,
	}); err != nil {
		return nil, errors.Wrapf(err, "failed to resume sandbox %s", in.SandboxID)
	}

	return &api.ControllerResumeResponse{}, nil
}

func (c *controllerLocal) Ping(ctx context.Context, in *api.ControllerPingRequest, opts ...grpc.CallOption) (*api.ControllerPingResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	if _, err := svc.PingSandbox(ctx, &proto.PingRequest{
		SandboxID: in.SandboxID,
	}); err != nil {
		return nil, err
	}

	return &api.ControllerPingResponse{}, nil
}

func (c *controllerLocal) Status(ctx context.Context, in *api.ControllerStatusRequest, opts ...grpc.CallOption) (*api.ControllerStatusResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	resp, err := svc.SandboxStatus(ctx, &proto.SandboxStatusRequest{SandboxID: in.SandboxID})
	if err != nil {
		return nil, fmt.Errorf("failed to query sandbox %s status: %w", in.SandboxID, err)
	}

	return &api.ControllerStatusResponse{Status: resp.Status}, nil
}

func (c *controllerLocal) getSandbox(ctx context.Context, id string) (task.SandboxService, error) {
	shim, err := c.shims.Get(ctx, id)
	if err != nil {
		return nil, errdefs.ErrNotFound
	}

	svc := task.NewSandboxClient(shim.Client())
	return svc, nil
}
