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

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (*runtime.ListPodSandboxMetricsResponse, error) {
	sandboxList := c.sandboxStore.List()
	//metricsList := c.sandboxStore.

	podMetrics := make([]*runtime.PodSandboxMetrics, 0)
	for _, sandbox := range sandboxList {
		c.containerStore.List()
		containerMetrics := make([]*runtime.ContainerMetrics, 0)

		podMetrics = append(podMetrics, &runtime.PodSandboxMetrics{
			PodSandboxId:     sandbox.ID,
			ContainerMetrics: containerMetrics,
		})
	}

	return &runtime.ListPodSandboxMetricsResponse{
		PodMetrics: podMetrics,
	}, nil
}

// gives the metrics for a given container in a sandbox
func (c *criService) listContainerMetrics(sandboxID string, containerID string) *runtime.ContainerMetrics {

}
