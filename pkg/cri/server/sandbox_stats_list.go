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
	"fmt"

	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ListPodSandboxStats returns stats of all ready sandboxes.
func (c *criService) ListPodSandboxStats(
	ctx context.Context,
	r *runtime.ListPodSandboxStatsRequest,
) (*runtime.ListPodSandboxStatsResponse, error) {
	sandboxes := c.sandboxesForListPodSandboxStatsRequest(r)

	podSandboxStats := new(runtime.ListPodSandboxStatsResponse)
	for _, sandbox := range sandboxes {
		metrics, err := metricsForSandbox(sandbox)
		if err != nil { //nolint:staticcheck // Ignore SA4023 as some platforms always return nil (unimplemented metrics)
			return nil, fmt.Errorf("failed to obtain metrics for sandbox %q: %w", sandbox.ID, err)
		}

		sandboxStats, err := c.podSandboxStats(ctx, sandbox, metrics)
		if err != nil { //nolint:staticcheck // Ignore SA4023 as some platforms always return nil (unimplemented metrics)
			return nil, fmt.Errorf("failed to decode sandbox container metrics for sandbox %q: %w", sandbox.ID, err)
		}
		podSandboxStats.Stats = append(podSandboxStats.Stats, sandboxStats)
	}

	return podSandboxStats, nil
}

func (c *criService) sandboxesForListPodSandboxStatsRequest(r *runtime.ListPodSandboxStatsRequest) []sandboxstore.Sandbox {
	sandboxesInStore := c.sandboxStore.List()

	if r.GetFilter() == nil {
		return sandboxesInStore
	}

	c.normalizePodSandboxStatsFilter(r.GetFilter())

	var sandboxes []sandboxstore.Sandbox
	for _, sandbox := range sandboxesInStore {
		if r.GetFilter().GetId() != "" && sandbox.ID != r.GetFilter().GetId() {
			continue
		}

		if r.GetFilter().GetLabelSelector() != nil &&
			!matchLabelSelector(r.GetFilter().GetLabelSelector(), sandbox.Config.GetLabels()) {
			continue
		}

		// We can't obtain metrics for sandboxes that aren't in ready state
		if sandbox.Status.Get().State != sandboxstore.StateReady {
			continue
		}

		sandboxes = append(sandboxes, sandbox)
	}

	return sandboxes
}
