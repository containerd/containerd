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

	runtimeAPI "github.com/containerd/containerd/api/runtime/sandbox/v1"
	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	v2 "github.com/containerd/containerd/runtime/v2"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/containerd/services"
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

func (c *controllerLocal) Create(ctx context.Context, in *api.ControllerCreateRequest, opts ...grpc.CallOption) (*api.ControllerCreateResponse, error) {
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

	svc := runtimeAPI.NewSandboxClient(shim.Client())

	if _, err := svc.CreateSandbox(ctx, &runtimeAPI.CreateSandboxRequest{
		SandboxID:  in.SandboxID,
		BundlePath: shim.Bundle(),
		Rootfs:     in.Rootfs,
		Options:    in.Options,
	}); err != nil {
		// TODO: Delete sandbox shim here.
		return nil, fmt.Errorf("failed to start sandbox %s: %w", in.SandboxID, err)
	}

	return &api.ControllerCreateResponse{SandboxID: in.SandboxID}, nil
}

func (c *controllerLocal) Start(ctx context.Context, in *api.ControllerStartRequest, opts ...grpc.CallOption) (*api.ControllerStartResponse, error) {
	shim, err := c.shims.Get(ctx, in.GetSandboxID())
	if err != nil {
		return nil, fmt.Errorf("unable to find sandbox %q", in.GetSandboxID())
	}

	svc := runtimeAPI.NewSandboxClient(shim.Client())
	resp, err := svc.StartSandbox(ctx, &runtimeAPI.StartSandboxRequest{SandboxID: in.SandboxID})
	if err != nil {
		return nil, fmt.Errorf("failed to start sandbox %s: %w", in.SandboxID, err)
	}

	return &api.ControllerStartResponse{
		SandboxID: in.SandboxID,
		Pid:       resp.Pid,
		CreatedAt: resp.CreatedAt,
	}, nil
}

func (c *controllerLocal) Stop(ctx context.Context, in *api.ControllerStopRequest, opts ...grpc.CallOption) (*api.ControllerStopResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	if _, err := svc.StopSandbox(ctx, &runtimeAPI.StopSandboxRequest{
		SandboxID:   in.SandboxID,
		TimeoutSecs: in.TimeoutSecs,
	}); err != nil {
		return nil, fmt.Errorf("failed to stop sandbox: %w", err)
	}

	return &api.ControllerStopResponse{}, nil
}

func (c *controllerLocal) Shutdown(ctx context.Context, in *api.ControllerShutdownRequest, opts ...grpc.CallOption) (*api.ControllerShutdownResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	_, err = svc.ShutdownSandbox(ctx, &runtimeAPI.ShutdownSandboxRequest{SandboxID: in.SandboxID})
	if err != nil {
		return nil, errdefs.ToGRPC(fmt.Errorf("failed to shutdown sandbox: %w", err))
	}

	if err := c.shims.Delete(ctx, in.SandboxID); err != nil {
		return nil, errdefs.ToGRPC(fmt.Errorf("failed to delete sandbox shim: %w", err))
	}

	return &api.ControllerShutdownResponse{}, nil
}

func (c *controllerLocal) Wait(ctx context.Context, in *api.ControllerWaitRequest, opts ...grpc.CallOption) (*api.ControllerWaitResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, err
	}

	resp, err := svc.WaitSandbox(ctx, &runtimeAPI.WaitSandboxRequest{
		SandboxID: in.SandboxID,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to wait sandbox %s: %w", in.SandboxID, err)
	}

	return &api.ControllerWaitResponse{
		ExitStatus: resp.ExitStatus,
		ExitedAt:   resp.ExitedAt,
	}, nil
}

func (c *controllerLocal) Status(ctx context.Context, in *api.ControllerStatusRequest, opts ...grpc.CallOption) (*api.ControllerStatusResponse, error) {
	svc, err := c.getSandbox(ctx, in.SandboxID)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	resp, err := svc.SandboxStatus(ctx, &runtimeAPI.SandboxStatusRequest{SandboxID: in.SandboxID})
	if err != nil {
		return nil, fmt.Errorf("failed to query sandbox %s status: %w", in.SandboxID, err)
	}

	return &api.ControllerStatusResponse{
		ID:       resp.ID,
		Pid:      resp.Pid,
		State:    resp.State,
		ExitedAt: resp.ExitedAt,
		Extra:    resp.Extra,
	}, nil
}

func (c *controllerLocal) getSandbox(ctx context.Context, id string) (runtimeAPI.SandboxService, error) {
	shim, err := c.shims.Get(ctx, id)
	if err != nil {
		return nil, errdefs.ErrNotFound
	}

	svc := runtimeAPI.NewSandboxClient(shim.Client())
	return svc, nil
}
