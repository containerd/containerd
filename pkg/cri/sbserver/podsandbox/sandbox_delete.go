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

package podsandbox

import (
	"context"
	"fmt"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd"
	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
)

func (c *Controller) Shutdown(ctx context.Context, sandboxID string) (*api.ControllerShutdownResponse, error) {
	sandbox, err := c.sandboxStore.Get(sandboxID)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %w", sandboxID, err)
		}
		// Do not return error if the id doesn't exist.
		log.G(ctx).Tracef("Sandbox controller Delete called for sandbox %q that does not exist", sandboxID)
		return &api.ControllerShutdownResponse{}, nil
	}

	// Cleanup the sandbox root directories.
	sandboxRootDir := c.getSandboxRootDir(sandboxID)
	if err := ensureRemoveAll(ctx, sandboxRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove sandbox root directory %q: %w", sandboxRootDir, err)
	}
	volatileSandboxRootDir := c.getVolatileSandboxRootDir(sandboxID)
	if err := ensureRemoveAll(ctx, volatileSandboxRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove volatile sandbox root directory %q: %w",
			volatileSandboxRootDir, err)
	}

	// Delete sandbox container.
	if sandbox.Container != nil {
		if err := sandbox.Container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
			if !errdefs.IsNotFound(err) {
				return nil, fmt.Errorf("failed to delete sandbox container %q: %w", sandboxID, err)
			}
			log.G(ctx).Tracef("Sandbox controller Delete called for sandbox container %q that does not exist", sandboxID)
		}
	}

	c.sandboxStore.Delete(sandboxID)

	// Send CONTAINER_DELETED event with ContainerId equal to SandboxId.
	c.cri.GenerateAndSendContainerEvent(ctx, sandboxID, sandboxID, runtime.ContainerEventType_CONTAINER_DELETED_EVENT)

	return &api.ControllerShutdownResponse{}, nil
}
