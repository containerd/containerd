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
	"errors"
	"fmt"
	"sync"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// StreamPodSandboxStats streams stats of all ready sandboxes matching the filter.
func (c *criService) StreamPodSandboxStats(r *runtime.StreamPodSandboxStatsRequest, s grpc.ServerStreamingServer[runtime.StreamPodSandboxStatsResponse]) error {
	ctx := s.Context()
	listReq := &runtime.ListPodSandboxStatsRequest{
		Filter: r.GetFilter(),
	}
	sandboxes := c.sandboxesForListPodSandboxStatsRequest(listReq)
	stats, errs := make([]*runtime.PodSandboxStats, len(sandboxes)), make([]error, len(sandboxes))

	var wg sync.WaitGroup
	for i, sandbox := range sandboxes {
		wg.Go(func() {
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
		})
	}
	wg.Wait()

	var podSandboxStats []*runtime.PodSandboxStats
	for _, stat := range stats {
		if stat != nil {
			podSandboxStats = append(podSandboxStats, stat)
		}
	}

	if err := sendInBatches(ctx, podSandboxStats, defaultStreamBatchSize, func(batch []*runtime.PodSandboxStats) error {
		return s.Send(&runtime.StreamPodSandboxStatsResponse{PodSandboxStats: batch})
	}); err != nil {
		return err
	}

	return errors.Join(errs...)
}
