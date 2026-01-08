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
	"fmt"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/server/podsandbox"
	sstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/log"
)

func (c *criService) UpdatePodSandboxResources(ctx context.Context, r *runtime.UpdatePodSandboxResourcesRequest) (*runtime.UpdatePodSandboxResourcesResponse, error) {
	span := tracing.SpanFromContext(ctx)
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox: %w", err)
	}

	span.SetAttributes(tracing.Attribute("sandbox.id", sandbox.ID))

	overhead := r.GetOverhead()
	resources := r.GetResources()
	err = c.nri.UpdatePodSandboxResources(ctx, &sandbox, overhead, resources)
	if err != nil {
		return nil, fmt.Errorf("NRI sandbox update failed: %w", err)
	}

	err = sandbox.Status.Update(
		func(status sstore.Status) (sstore.Status, error) {
			status.Overhead = &runtime.ContainerResources{
				Linux: overhead,
			}
			status.Resources = &runtime.ContainerResources{
				Linux: resources,
			}
			return status, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update sandbox store: %w", err)
	}

	sandboxInfo, err := c.client.SandboxStore().Get(ctx, sandbox.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox %s from sandbox store: %w", sandbox.ID, err)
	}

	updatedRes := podsandbox.UpdatedResources{
		Overhead:  overhead,
		Resources: resources,
	}
	if err := sandboxInfo.AddExtension(podsandbox.UpdatedResourcesKey, &updatedRes); err != nil {
		return nil, fmt.Errorf("failed to add updated sandbox resources extension: %w", err)
	}
	if _, err := c.client.SandboxStore().Update(ctx, sandboxInfo, "extensions"); err != nil {
		return nil, fmt.Errorf("failed to update sandbox %s in core store: %w", sandbox.ID, err)
	}

	err = c.nri.PostUpdatePodSandboxResources(ctx, &sandbox)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("NRI post-update notification failed")
	}

	return &runtime.UpdatePodSandboxResourcesResponse{}, nil
}
