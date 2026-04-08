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
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// StreamPodSandboxMetrics streams pod sandbox metrics.
func (c *criService) StreamPodSandboxMetrics(r *runtime.StreamPodSandboxMetricsRequest, s grpc.ServerStreamingServer[runtime.StreamPodSandboxMetricsResponse]) error {
	ctx := s.Context()
	resp, err := c.ListPodSandboxMetrics(ctx, &runtime.ListPodSandboxMetricsRequest{})
	if err != nil {
		return err
	}

	return sendInBatches(ctx, resp.PodMetrics, defaultStreamBatchSize, func(batch []*runtime.PodSandboxMetrics) error {
		return s.Send(&runtime.StreamPodSandboxMetricsResponse{PodSandboxMetrics: batch})
	})
}
