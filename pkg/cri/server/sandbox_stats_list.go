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

	"github.com/containerd/containerd/errdefs"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ListPodSandboxStats returns stats of all ready sandboxes.
func (c *criService) ListPodSandboxStats(
	ctx context.Context,
	r *runtime.ListPodSandboxStatsRequest,
) (*runtime.ListPodSandboxStatsResponse, error) {
	sandboxes := c.sandboxesForListPodSandboxStatsRequest(r)

	var errs []error
	podSandboxStats := new(runtime.ListPodSandboxStatsResponse)
	for _, sandbox := range sandboxes {
		sandboxStats, err := c.podSandboxStats(ctx, sandbox)
		switch {
		case errdefs.IsUnavailable(err):
			log.G(ctx).WithField("podsandboxid", sandbox.ID).Debugf("failed to get pod sandbox stats, this is likely a transient error: %v", err)
		case err != nil:
			errs = append(errs, fmt.Errorf("failed to decode sandbox container metrics for sandbox %q: %w", sandbox.ID, err))
		default:
			podSandboxStats.Stats = append(podSandboxStats.Stats, sandboxStats)
		}
	}

	return podSandboxStats, errors.Join(errs...)
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
