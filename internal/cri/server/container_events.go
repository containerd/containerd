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
	"sync"

	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type eventQueue struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []*runtime.ContainerEventResponse
}

func newEventQueue() *eventQueue {
	q := &eventQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *eventQueue) enqueue(evt *runtime.ContainerEventResponse) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.buf = append(q.buf, evt)
	if len(q.buf) >= 100 {
		log.G(context.Background()).Warnf("eventQueue buffer warning, current size=%d", len(q.buf))
	}
	q.cond.Signal()
}

func (q *eventQueue) dequeue() *runtime.ContainerEventResponse {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.buf) == 0 {
		q.cond.Wait()
	}
	evt := q.buf[0]
	q.buf[0] = nil
	q.buf = q.buf[1:]
	return evt
}

func (c *criService) GetContainerEvents(r *runtime.GetEventsRequest, s runtime.RuntimeService_GetContainerEventsServer) error {
	eventC, closer := c.containerEventsQ.Subscribe()
	defer closer.Close()

	eq := newEventQueue()
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		for {
			evt := eq.dequeue()
			if err := s.Send(evt); err != nil {
				errCh <- err
				return
			}
		}
	}()

	for event := range eventC {
		eq.enqueue(event)
	}
	return <-errCh
}
