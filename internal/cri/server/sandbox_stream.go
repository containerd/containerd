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
	"time"

	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// StreamPodSandboxes streams all pod sandboxes matching the filter.
func (c *criService) StreamPodSandboxes(r *runtime.StreamPodSandboxesRequest, s grpc.ServerStreamingServer[runtime.StreamPodSandboxesResponse]) error {
	ctx := s.Context()
	start := time.Now()
	sandboxesInStore := c.sandboxStore.List()

	var sandboxes []*runtime.PodSandbox
	for _, sandboxInStore := range sandboxesInStore {
		sandboxes = append(sandboxes, toCRISandbox(
			sandboxInStore.Metadata,
			sandboxInStore.Status.Get(),
		))
	}

	sandboxes = c.filterCRISandboxes(sandboxes, r.GetFilter())

	err := sendInBatches(ctx, sandboxes, defaultStreamBatchSize, func(batch []*runtime.PodSandbox) error {
		return s.Send(&runtime.StreamPodSandboxesResponse{PodSandboxes: batch})
	})

	sandboxStreamTimer.UpdateSince(start)
	return err
}
