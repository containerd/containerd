//go:build linux

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

package task

import (
	"context"
	"sync"
	"testing"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/pkg/shutdown"
)

type fakePublisher struct {
	mu        sync.Mutex
	delay     time.Duration
	block     chan struct{}
	published []events.Event
	closed    bool
}

func (p *fakePublisher) Publish(ctx context.Context, topic string, event events.Event) error {
	if p.block != nil {
		<-p.block
	}
	time.Sleep(p.delay)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, event)
	return nil
}

func (p *fakePublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *fakePublisher) events() ([]events.Event, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]events.Event(nil), p.published...), p.closed
}

func newForwardingService(t *testing.T, publisher *fakePublisher) *service {
	ctx, sd := shutdown.WithShutdown(context.Background())
	t.Cleanup(sd.Shutdown)
	s := &service{
		context:     ctx,
		events:      make(chan any, 128),
		shutdown:    sd,
		stopForward: make(chan struct{}),
		forwardDone: make(chan struct{}),
	}
	go s.forward(ctx, publisher)
	return s
}

func TestStopForwardingPublishesQueuedEvents(t *testing.T) {
	publisher := &fakePublisher{delay: 10 * time.Millisecond}
	s := newForwardingService(t, publisher)

	sent := []events.Event{
		&eventstypes.TaskExit{ContainerID: "c1"},
		&eventstypes.TaskDelete{ContainerID: "c1"},
		&eventstypes.TaskExit{ContainerID: "c2"},
		&eventstypes.TaskDelete{ContainerID: "c2"},
	}
	for _, e := range sent {
		s.send(e)
	}
	require.NoError(t, s.stopForwarding(context.Background()))

	published, closed := publisher.events()
	require.Equal(t, sent, published, "queued events must be published, in order, before the shim exits")
	require.True(t, closed)
}

func TestStopForwardingTimeout(t *testing.T) {
	publisher := &fakePublisher{block: make(chan struct{})}
	defer close(publisher.block)
	s := newForwardingService(t, publisher)

	s.send(&eventstypes.TaskExit{ContainerID: "c1"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// A timeout must not fail the shutdown.
	require.NoError(t, s.stopForwarding(ctx))
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
