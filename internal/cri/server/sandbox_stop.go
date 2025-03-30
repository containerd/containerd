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

package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/errdefs"
)

// StopPodSandbox stops the sandbox. If there are any running containers in the
// sandbox, they should be forcibly terminated.
func (c *criService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	span := tracing.SpanFromContext(ctx)
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %w",
				r.GetPodSandboxId(), err)
		}

		// The StopPodSandbox RPC is idempotent, and must not return an error
		// if all relevant resources have already been reclaimed. Ref:
		// https://github.com/kubernetes/cri-api/blob/c20fa40/pkg/apis/runtime/v1/api.proto#L45-L46
		return &runtime.StopPodSandboxResponse{}, nil
	}

	defer c.nri.BlockPluginSync().Unblock()

	span.SetAttributes(tracing.Attribute("sandbox.id", sandbox.ID))
	if err := c.stopPodSandbox(ctx, sandbox); err != nil {
		return nil, err
	}

	return &runtime.StopPodSandboxResponse{}, nil
}

func (c *criService) stopPodSandbox(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	span := tracing.SpanFromContext(ctx)
	// Use the full sandbox id.
	id := sandbox.ID

	// Stop all containers inside the sandbox. This terminates the container forcibly,
	// and container may still be created, so production should not rely on this behavior.
	// TODO(random-liu): Introduce a state in sandbox to avoid future container creation.
	span.AddEvent("stopping containers in the sandbox")
	stop := time.Now()
	containers := c.containerStore.List()
	for _, container := range containers {
		if container.SandboxID != id {
			continue
		}
		// Forcibly stop the container. Do not use `StopContainer`, because it introduces a race
		// if a container is removed after list.
		if err := c.stopContainer(ctx, container, 0); err != nil {
			return fmt.Errorf("failed to stop container %q: %w", container.ID, err)
		}
	}

	// Only stop sandbox container when it's running or unknown.
	state := sandbox.Status.Get().State
	if state == sandboxstore.StateReady || state == sandboxstore.StateUnknown {
		if err := c.sandboxService.StopSandbox(ctx, sandbox.Sandboxer, id); err != nil {
			// Log and ignore the error if controller already removed the sandbox
			if errdefs.IsNotFound(err) {
				log.G(ctx).Warnf("sandbox %q is not found when stopping it", id)
			} else {
				return fmt.Errorf("failed to stop sandbox %q: %w", id, err)
			}
		}
	}

	sandboxRuntimeStopTimer.WithValues(sandbox.RuntimeHandler).UpdateSince(stop)

	span.AddEvent("sandbox container stopped",
		tracing.Attribute("sandbox.stop.duration", time.Since(stop).String()),
	)

	err := c.nri.StopPodSandbox(ctx, &sandbox)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("NRI sandbox stop notification failed")
	}

	// Teardown network for sandbox.
	if sandbox.NetNS != nil {
		span.AddEvent("start pod network teardown")
		netStop := time.Now()
		// Use empty netns path if netns is not available. This is defined in:
		// https://github.com/containernetworking/cni/blob/v0.7.0-alpha1/SPEC.md
		if closed, err := sandbox.NetNS.Closed(); err != nil {
			return fmt.Errorf("failed to check network namespace closed: %w", err)
		} else if closed {
			sandbox.NetNSPath = ""
		}
		if sandbox.CNIResult != nil {
			if err := c.teardownPodNetwork(ctx, sandbox); err != nil {
				return fmt.Errorf("failed to destroy network for sandbox %q: %w", id, err)
			}
		}
		if err := sandbox.NetNS.Remove(); err != nil {
			return fmt.Errorf("failed to remove network namespace for sandbox %q: %w", id, err)
		}
		sandboxDeleteNetwork.UpdateSince(netStop)

		span.AddEvent("finished pod network teardown",
			tracing.Attribute("network.teardown.duration", time.Since(netStop).String()),
		)
	}

	log.G(ctx).Infof("TearDown network for sandbox %q successfully", id)

	err = c.cleanupImageMounts(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to cleanup image mounts for sandbox %q: %w", id, err)
	}
	return nil
}

// waitSandboxStop waits for sandbox to be stopped until context is cancelled or
// the context deadline is exceeded.
func (c *criService) waitSandboxStop(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait sandbox container %q: %w", sandbox.ID, ctx.Err())
	case <-sandbox.Stopped():
		return nil
	}
}

// teardownPodNetwork removes the network from the pod
func (c *criService) teardownPodNetwork(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	netPlugin := c.getNetworkPlugin(sandbox.RuntimeHandler)
	if netPlugin == nil {
		return errors.New("cni config not initialized")
	}

	var (
		id     = sandbox.ID
		path   = sandbox.NetNSPath
		config = sandbox.Config
	)
	opts, err := cniNamespaceOpts(id, config)
	if err != nil {
		return fmt.Errorf("get cni namespace options: %w", err)
	}

	netStart := time.Now()
	err = netPlugin.Remove(ctx, id, path, opts...)
	networkPluginOperations.WithValues(networkTearDownOp).Inc()
	networkPluginOperationsLatency.WithValues(networkTearDownOp).UpdateSince(netStart)
	if err != nil {
		networkPluginOperationsErrors.WithValues(networkTearDownOp).Inc()
		return err
	}
	return nil
}
