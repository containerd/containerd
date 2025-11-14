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
	"sync"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ListPodSandboxStats returns stats of all ready sandboxes.
func (c *criService) ListPodSandboxStats(
	ctx context.Context,
	r *runtime.ListPodSandboxStatsRequest,
) (*runtime.ListPodSandboxStatsResponse, error) {
	sandboxes := c.sandboxesForListPodSandboxStatsRequest(r)
	stats, errs := make([]*runtime.PodSandboxStats, len(sandboxes)), make([]error, len(sandboxes))

	var wg sync.WaitGroup
	for i, sandbox := range sandboxes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// issue 12279: this is tautologically true where interface is stubbed out.
			sandboxStats, err := c.podSandboxStats(ctx, sandbox) //nolint: staticcheck
			switch {
			case errdefs.IsUnavailable(err), errdefs.IsNotFound(err):
				log.G(ctx).WithField("podsandboxid", sandbox.ID).WithError(err).Debug("failed to get pod sandbox stats, this is likely a transient error")
			case errors.Is(err, ttrpc.ErrClosed):
				log.G(ctx).WithField("podsandboxid", sandbox.ID).WithError(err).Debug("failed to get pod sandbox stats, connection closed")
			case err != nil: //nolint: staticcheck
				errs[i] = fmt.Errorf("failed to decode sandbox container metrics for sandbox %q: %w", sandbox.ID, err)
			default:
				stats[i] = sandboxStats
			}
		}()
	}
	wg.Wait()

	podSandboxStats := new(runtime.ListPodSandboxStatsResponse)
	for _, stat := range stats {
		if stat != nil {
			podSandboxStats.Stats = append(podSandboxStats.Stats, stat)
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
