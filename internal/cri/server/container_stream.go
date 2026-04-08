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

// StreamContainers streams all containers matching the filter.
func (c *criService) StreamContainers(r *runtime.StreamContainersRequest, s grpc.ServerStreamingServer[runtime.StreamContainersResponse]) error {
	ctx := s.Context()
	start := time.Now()
	containersInStore := c.containerStore.List()

	var containers []*runtime.Container
	for _, container := range containersInStore {
		containers = append(containers, toCRIContainer(container))
	}

	containers = c.filterCRIContainers(containers, r.GetFilter())

	err := sendInBatches(ctx, containers, defaultStreamBatchSize, func(batch []*runtime.Container) error {
		return s.Send(&runtime.StreamContainersResponse{Containers: batch})
	})

	containerStreamTimer.UpdateSince(start)
	return err
}
