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

package sbserver

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/sirupsen/logrus"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// RemovePodSandbox removes the sandbox. If there are running containers in the
// sandbox, they should be forcibly removed.
func (c *criService) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	start := time.Now()
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %w",
				r.GetPodSandboxId(), err)
		}
		// Do not return error if the id doesn't exist.
		log.G(ctx).Tracef("RemovePodSandbox called for sandbox %q that does not exist",
			r.GetPodSandboxId())
		return &runtime.RemovePodSandboxResponse{}, nil
	}
	// Use the full sandbox id.
	id := sandbox.ID

	// If the sandbox is still running or in an unknown state, forcibly stop it.
	state := sandbox.Status.Get().State
	if state == sandboxstore.StateReady || state == sandboxstore.StateUnknown {
		logrus.Infof("Forcibly stopping sandbox %q", id)
		if err := c.stopPodSandbox(ctx, sandbox); err != nil {
			return nil, fmt.Errorf("failed to forcibly stop sandbox %q: %v", id, err)
		}
	}

	// Return error if sandbox network namespace is not closed yet.
	if sandbox.NetNS != nil {
		nsPath := sandbox.NetNS.GetPath()
		if closed, err := sandbox.NetNS.Closed(); err != nil {
			return nil, fmt.Errorf("failed to check sandbox network namespace %q closed: %w", nsPath, err)
		} else if !closed {
			return nil, fmt.Errorf("sandbox network namespace %q is not fully closed", nsPath)
		}
	}

	// Remove all containers inside the sandbox.
	// NOTE(random-liu): container could still be created after this point, Kubelet should
	// not rely on this behavior.
	// TODO(random-liu): Introduce an intermediate state to avoid container creation after
	// this point.
	cntrs := c.containerStore.List()
	for _, cntr := range cntrs {
		if cntr.SandboxID != id {
			continue
		}
		_, err = c.RemoveContainer(ctx, &runtime.RemoveContainerRequest{ContainerId: cntr.ID})
		if err != nil {
			return nil, fmt.Errorf("failed to remove container %q: %w", cntr.ID, err)
		}
	}

	// Cleanup the sandbox root directories.
	sandboxRootDir := c.getSandboxRootDir(id)
	if err := ensureRemoveAll(ctx, sandboxRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove sandbox root directory %q: %w",
			sandboxRootDir, err)
	}
	volatileSandboxRootDir := c.getVolatileSandboxRootDir(id)
	if err := ensureRemoveAll(ctx, volatileSandboxRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove volatile sandbox root directory %q: %w",
			volatileSandboxRootDir, err)
	}
	sandboxInstance, err := c.client.LoadSandbox(ctx, sandbox.Sandboxer, sandbox.ID)
	if err != nil && !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to load sandbox by id %q, %w", sandbox.ID, err)
	}
	if sandboxInstance != nil {
		if err := sandboxInstance.Delete(ctx); err != nil && !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to delete sandbox by id %q, %w", sandbox.ID, err)
		}
	}
	// Remove sandbox from sandbox store. Note that once the sandbox is successfully
	// deleted:
	// 1) ListPodSandbox will not include this sandbox.
	// 2) PodSandboxStatus and StopPodSandbox will return error.
	// 3) On-going operations which have held the reference will not be affected.
	c.sandboxStore.Delete(id)

	// Release the sandbox name reserved for the sandbox.
	c.sandboxNameIndex.ReleaseByKey(id)

	sandboxRemoveTimer.WithValues(sandbox.RuntimeHandler).UpdateSince(start)

	return &runtime.RemovePodSandboxResponse{}, nil
}
