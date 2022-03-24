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

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ContainerStats returns stats of the container. If the container does not
// exist, the call returns an error.
func (c *criService) ContainerStats(ctx context.Context, in *runtime.ContainerStatsRequest) (*runtime.ContainerStatsResponse, error) {
	cntr, err := c.containerStore.Get(in.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("failed to find container: %w", err)
	}
	request := &tasks.MetricsRequest{Filters: []string{"id==" + cntr.ID}}
	resp, err := c.client.TaskService().Metrics(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics for task: %w", err)
	}
	if len(resp.Metrics) != 1 {
		return nil, fmt.Errorf("unexpected metrics response: %+v", resp.Metrics)
	}

	cs, err := c.containerMetrics(cntr.Metadata, resp.Metrics[0])
	if err != nil { //nolint:staticcheck // Ignore SA4023 as some platforms always return nil (stats unimplemented)
		return nil, fmt.Errorf("failed to decode container metrics: %w", err)
	}
	return &runtime.ContainerStatsResponse{Stats: cs}, nil
}
