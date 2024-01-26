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

	runtimeAPI "github.com/containerd/containerd/v2/api/runtime/sandbox/v1"
	"github.com/containerd/containerd/v2/api/types"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/events"
	"github.com/containerd/containerd/v2/events/exchange"
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/runtime"
	v2 "github.com/containerd/containerd/v2/runtime/v2"
	"github.com/containerd/containerd/v2/sandbox"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"google.golang.org/protobuf/types/known/anypb"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.SandboxControllerPlugin,
		ID:   "shim",
		Requires: []plugin.Type{
			plugins.RuntimePluginV2,
			plugins.EventPlugin,
			plugins.SandboxStorePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			shimPlugin, err := ic.GetByID(plugins.RuntimePluginV2, "shim")
			if err != nil {
				return nil, err
			}

			exchangePlugin, err := ic.GetByID(plugins.EventPlugin, "exchange")
			if err != nil {
				return nil, err
			}

			var (
				shims     = shimPlugin.(*v2.ShimManager)
				publisher = exchangePlugin.(*exchange.Exchange)
			)

			return &controllerLocal{
				shims:     shims,
				publisher: publisher,
			}, nil
		},
	})
}

type controllerLocal struct {
	shims     *v2.ShimManager
	publisher events.Publisher
}

var _ sandbox.Controller = (*controllerLocal)(nil)

func (c *controllerLocal) cleanupShim(ctx context.Context, sandboxID string, svc runtimeAPI.TTRPCSandboxService) {
	// Let the shim exit, then we can clean up the bundle after.
	if _, sErr := svc.ShutdownSandbox(ctx, &runtimeAPI.ShutdownSandboxRequest{
		SandboxID: sandboxID,
	}); sErr != nil {
		log.G(ctx).WithError(sErr).WithField("sandboxID", sandboxID).
			Error("failed to shutdown sandbox")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	dErr := c.shims.Delete(ctx, sandboxID)
	if dErr != nil {
		log.G(ctx).WithError(dErr).WithField("sandboxID", sandboxID).
			Error("failed to delete shim")
	}
}

func (c *controllerLocal) Create(ctx context.Context, info sandbox.Sandbox, opts ...sandbox.CreateOpt) error {
	var coptions sandbox.CreateOptions
	sandboxID := info.ID
	for _, opt := range opts {
		opt(&coptions)
	}

	if _, err := c.shims.Get(ctx, sandboxID); err == nil {
		return fmt.Errorf("sandbox %s already running: %w", sandboxID, errdefs.ErrAlreadyExists)
	}

	shim, err := c.shims.Start(ctx, sandboxID, runtime.CreateOpts{
		Spec:           info.Spec,
		RuntimeOptions: info.Runtime.Options,
		Runtime:        info.Runtime.Name,
		TaskOptions:    nil,
	})
	if err != nil {
		return fmt.Errorf("failed to start new shim for sandbox %s: %w", sandboxID, err)
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
		Rootfs:     mount.ToProto(coptions.Rootfs),
		Options:    options,
		NetnsPath:  coptions.NetNSPath,
	}); err != nil {
		c.cleanupShim(ctx, sandboxID, svc)
		return fmt.Errorf("failed to create sandbox %s: %w", sandboxID, errdefs.FromGRPC(err))
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
		c.cleanupShim(ctx, sandboxID, svc)
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
	if errdefs.IsNotFound(err) {
		return sandbox.ControllerStatus{
			SandboxID: sandboxID,
			ExitedAt:  time.Now(),
		}, nil
	}
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
		Info:      resp.GetInfo(),
		CreatedAt: resp.GetCreatedAt().AsTime(),
		ExitedAt:  resp.GetExitedAt().AsTime(),
		Extra:     resp.GetExtra(),
	}, nil
}

func (c *controllerLocal) Metrics(ctx context.Context, sandboxID string) (*types.Metric, error) {
	sb, err := c.getSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	req := &runtimeAPI.SandboxMetricsRequest{SandboxID: sandboxID}
	resp, err := sb.SandboxMetrics(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Metrics, nil
}

func (c *controllerLocal) getSandbox(ctx context.Context, id string) (runtimeAPI.TTRPCSandboxService, error) {
	shim, err := c.shims.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	return sandbox.NewClient(shim.Client())
}
