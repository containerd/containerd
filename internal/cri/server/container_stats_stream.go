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

	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// StreamContainerStats streams stats of all containers matching the filter.
func (c *criService) StreamContainerStats(r *runtime.StreamContainerStatsRequest, s grpc.ServerStreamingServer[runtime.StreamContainerStatsResponse]) error {
	ctx := s.Context()
	listReq := &runtime.ListContainerStatsRequest{
		Filter: r.GetFilter(),
	}
	css, err := c.listContainerStats(ctx, listReq)
	if err != nil {
		return fmt.Errorf("failed to fetch containers and stats: %w", err)
	}

	resp := c.toCRIContainerStats(css)

	return sendInBatches(ctx, resp.Stats, defaultStreamBatchSize, func(batch []*runtime.ContainerStats) error {
		return s.Send(&runtime.StreamContainerStatsResponse{ContainerStats: batch})
	})
}
