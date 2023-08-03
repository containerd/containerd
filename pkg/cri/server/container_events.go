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
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) GetContainerEvents(r *runtime.GetEventsRequest, s runtime.RuntimeService_GetContainerEventsServer) error {
	errCh := make(chan error, 1)

	c.containerEventsClients.Store(s, errCh)

	err := <-errCh

	c.containerEventsClients.Delete(s)

	return err
}

func (c *criService) broadcastEvents() {
	for containerEvent := range c.containerEventsChan {
		c.containerEventsClients.Range(func(key, value any) bool {
			stream, ok := key.(runtime.RuntimeService_GetContainerEventsServer)
			if !ok {
				return true
			}

			errCh, ok := value.(chan error)
			if !ok {
				return true
			}

			select {
			case <-stream.Context().Done():
				errCh <- stream.Context().Err()
			default:
				if err := stream.Send(&containerEvent); err != nil {
					errCh <- err
				}
			}

			return true
		})
	}
}
