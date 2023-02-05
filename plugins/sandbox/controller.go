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
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	v2 "github.com/containerd/containerd/runtime/v2"
	"github.com/containerd/containerd/sandbox"

	"google.golang.org/protobuf/types/known/anypb"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.SandboxControllerPlugin,
		ID:   "local",
		Requires: []plugin.Type{
			plugin.RuntimePluginV2,
			plugin.EventPlugin,
			plugin.SandboxStorePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			shimPlugin, err := ic.GetByID(plugin.RuntimePluginV2, "shim")
			if err != nil {
				return nil, err
			}

			exchangePlugin, err := ic.GetByID(plugin.EventPlugin, "exchange")
			if err != nil {
				return nil, err
			}

			sbPlugin, err := ic.GetByID(plugin.SandboxStorePlugin, "local")
			if err != nil {
				return nil, err
			}

			var (
				shims     = shimPlugin.(*v2.ShimManager)
				publisher = exchangePlugin.(*exchange.Exchange)
				store     = sbPlugin.(sandbox.Store)
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

var _ sandbox.Controller = (*controllerLocal)(nil)

func (c *controllerLocal) Create(ctx context.Context, sandboxID string, opts ...sandbox.CreateOpt) error {
	var coptions sandbox.CreateOptions
	for _, opt := range opts {
		opt(&coptions)
	}

	if _, err := c.shims.Get(ctx, sandboxID); err == nil {
		return fmt.Errorf("sandbox %s already running: %w", sandboxID, errdefs.ErrAlreadyExists)
	}

	info, err := c.store.Get(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to query sandbox metadata from store: %w", err)
	}

	shim, err := c.shims.Start(ctx, sandboxID, runtime.CreateOpts{
		Spec:           info.Spec,
		RuntimeOptions: info.Runtime.Options,
		Runtime:        info.Runtime.Name,
		TaskOptions:    nil,
	})

	if err != nil {
		return fmt.Errorf("failed to start new sandbox: %w", err)
	}

	svc, err := sandbox.NewClient(shim.Client())
	if err != nil {
		return err
	}

	var options *anypb.Any
	if coptions.Options != nil {
		options = &anypb.Any{
			TypeUrl: coptions.Options.GetTypeUrl(),
			Value:   coptions.Options.GetValue(),
		}
	}

	if _, err := svc.CreateSandbox(ctx, &runtimeAPI.CreateSandboxRequest{
		SandboxID:  sandboxID,
		BundlePath: shim.Bundle(),
		Rootfs:     coptions.Rootfs,
		Options:    options,
	}); err != nil {
		// TODO: Delete sandbox shim here.
		return fmt.Errorf("failed to start sandbox %s: %w", sandboxID, errdefs.FromGRPC(err))
	}

	return nil
}

func (c *controllerLocal) Start(ctx context.Context, sandboxID string) (sandbox.ControllerInstance, error) {
	shim, err := c.shims.Get(ctx, sandboxID)
	if err != nil {
		return sandbox.ControllerInstance{}, fmt.Errorf("unable to find sandbox %q", sandboxID)
	}

	svc, err := sandbox.NewClient(shim.Client())
	if err != nil {
		return sandbox.ControllerInstance{}, err
	}

	resp, err := svc.StartSandbox(ctx, &runtimeAPI.StartSandboxRequest{SandboxID: sandboxID})
	if err != nil {
		return sandbox.ControllerInstance{}, fmt.Errorf("failed to start sandbox %s: %w", sandboxID, errdefs.FromGRPC(err))
	}

	return sandbox.ControllerInstance{
		SandboxID: sandboxID,
		Pid:       resp.GetPid(),
		CreatedAt: resp.GetCreatedAt().AsTime(),
	}, nil
}

func (c *controllerLocal) Platform(ctx context.Context, sandboxID string) (platforms.Platform, error) {
	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return platforms.Platform{}, err
	}

	response, err := svc.Platform(ctx, &runtimeAPI.PlatformRequest{SandboxID: sandboxID})
	if err != nil {
		return platforms.Platform{}, fmt.Errorf("failed to get sandbox platform: %w", errdefs.FromGRPC(err))
	}

	var platform platforms.Platform
	if p := response.GetPlatform(); p != nil {
		platform.OS = p.OS
		platform.Architecture = p.Architecture
		platform.Variant = p.Variant
	}
	return platform, nil
}

func (c *controllerLocal) Stop(ctx context.Context, sandboxID string, opts ...sandbox.StopOpt) error {
	var soptions sandbox.StopOptions
	for _, opt := range opts {
		opt(&soptions)
	}
	req := &runtimeAPI.StopSandboxRequest{SandboxID: sandboxID}
	if soptions.Timeout != nil {
		req.TimeoutSecs = uint32(soptions.Timeout.Seconds())
	}

	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}

	if _, err := svc.StopSandbox(ctx, req); err != nil {
		return fmt.Errorf("failed to stop sandbox: %w", errdefs.FromGRPC(err))
	}

	return nil
}

func (c *controllerLocal) Shutdown(ctx context.Context, sandboxID string) error {
	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}

	_, err = svc.ShutdownSandbox(ctx, &runtimeAPI.ShutdownSandboxRequest{SandboxID: sandboxID})
	if err != nil {
		return fmt.Errorf("failed to shutdown sandbox: %w", errdefs.FromGRPC(err))
	}

	if err := c.shims.Delete(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to delete sandbox shim: %w", err)
	}

	return nil
}

func (c *controllerLocal) Wait(ctx context.Context, sandboxID string) (sandbox.ExitStatus, error) {
	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return sandbox.ExitStatus{}, err
	}

	resp, err := svc.WaitSandbox(ctx, &runtimeAPI.WaitSandboxRequest{
		SandboxID: sandboxID,
	})

	if err != nil {
		return sandbox.ExitStatus{}, fmt.Errorf("failed to wait sandbox %s: %w", sandboxID, errdefs.FromGRPC(err))
	}

	return sandbox.ExitStatus{
		ExitStatus: resp.GetExitStatus(),
		ExitedAt:   resp.GetExitedAt().AsTime(),
	}, nil
}

func (c *controllerLocal) Status(ctx context.Context, sandboxID string, verbose bool) (sandbox.ControllerStatus, error) {
	svc, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return sandbox.ControllerStatus{}, err
	}

	resp, err := svc.SandboxStatus(ctx, &runtimeAPI.SandboxStatusRequest{
		SandboxID: sandboxID,
		Verbose:   verbose,
	})
	if err != nil {
		return sandbox.ControllerStatus{}, fmt.Errorf("failed to query sandbox %s status: %w", sandboxID, err)
	}

	return sandbox.ControllerStatus{
		SandboxID: resp.GetSandboxID(),
		Pid:       resp.GetPid(),
		State:     resp.GetState(),
		ExitedAt:  resp.GetCreatedAt().AsTime(),
		Extra:     resp.GetExtra(),
	}, nil
}

func (c *controllerLocal) getSandbox(ctx context.Context, id string) (runtimeAPI.TTRPCSandboxService, error) {
	shim, err := c.shims.Get(ctx, id)
	if err != nil {
		return nil, errdefs.ErrNotFound
	}

	return sandbox.NewClient(shim.Client())
}
