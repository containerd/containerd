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

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/netns"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
)

// StopPodSandbox stops the sandbox. If there are any running containers in the
// sandbox, they should be forcibly terminated.
func (c *criService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %w",
			r.GetPodSandboxId(), err)
	}

	if err := c.stopPodSandbox(ctx, sandbox.ID); err != nil {
		return nil, err
	}

	return &runtime.StopPodSandboxResponse{}, nil
}

func (c *criService) stopPodSandbox(ctx context.Context, id string) error {
	sandboxInfo, err := c.client.SandboxStore().Get(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get sandbox %q metadata: %w", id, err)
	}

	// Stop all containers inside the sandbox. This terminates the container forcibly,
	// and container may still be created, so production should not rely on this behavior.
	// TODO(random-liu): Introduce a state in sandbox to avoid future container creation.
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

	if err := c.sandboxController.Shutdown(ctx, id); err != nil {
		return fmt.Errorf("failed to shutdown sandbox %q: %w", id, err)
	}

	sandboxRuntimeStopTimer.WithValues(sandboxInfo.Runtime.Name).UpdateSince(stop)

	var networkMetadata sandboxstore.PodNetworkMetadata
	err = sandboxInfo.GetExtension("network", &networkMetadata)
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to sandbox %q network: %w", id, err)
	}

	// Teardown network for sandbox.
	if err == nil {
		netNS := netns.LoadNetNS(networkMetadata.NetNSPath)

		netStop := time.Now()
		// Use empty netns path if netns is not available. This is defined in:
		// https://github.com/containernetworking/cni/blob/v0.7.0-alpha1/SPEC.md
		if closed, err := netNS.Closed(); err != nil {
			return fmt.Errorf("failed to check network namespace closed: %w", err)
		} else if closed {
			networkMetadata.NetNSPath = ""
		}

		var config runtime.PodSandboxConfig
		if err := sandboxInfo.GetExtension("config", &config); err != nil {
			return fmt.Errorf("failed to get sandbox %q config: %w", id, err)
		}

		if err := c.teardownPodNetwork(ctx, id, sandboxInfo.Runtime.Name, networkMetadata.NetNSPath, &config); err != nil {
			return fmt.Errorf("failed to destroy network for sandbox %q: %w", id, err)
		}
		if err := netNS.Remove(); err != nil {
			return fmt.Errorf("failed to remove network namespace for sandbox %q: %w", id, err)
		}
		sandboxDeleteNetwork.UpdateSince(netStop)
	}

	log.G(ctx).Infof("TearDown network for sandbox %q successfully", id)

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
func (c *criService) teardownPodNetwork(ctx context.Context, id, runtimeHandler, netNSPath string, config *runtime.PodSandboxConfig) error {
	netPlugin := c.getNetworkPlugin(runtimeHandler)
	if netPlugin == nil {
		return errors.New("cni config not initialized")
	}

	opts, err := cniNamespaceOpts(id, config)
	if err != nil {
		return fmt.Errorf("get cni namespace options: %w", err)
	}

	return netPlugin.Remove(ctx, id, netNSPath, opts...)
}
