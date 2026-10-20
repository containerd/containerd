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

	"github.com/containerd/console"
	eventstypes "github.com/containerd/containerd/api/events"
	runcC "github.com/containerd/go-runc"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/runc"
	"github.com/containerd/containerd/v2/core/events"
	oomv2 "github.com/containerd/containerd/v2/internal/oom"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/pkg/stdio"
)

type fakeOOM struct {
	stop func(containerID string)
}

func (f *fakeOOM) Add(string, int, oomv2.EventFunc) error { return nil }

func (f *fakeOOM) Stop(containerID string) error {
	f.stop(containerID)
	return nil
}

type fakePlatform struct{}

func (fakePlatform) CopyConsole(context.Context, console.Console, string, string, string, string, *sync.WaitGroup) (console.Console, error) {
	return nil, nil
}

func (fakePlatform) ShutdownConsole(context.Context, console.Console) error { return nil }

func (fakePlatform) Close() error { return nil }

func newExitTestService(t *testing.T) (*service, *runc.Container, *process.Init) {
	ctx, sd := shutdown.WithShutdown(context.Background())
	t.Cleanup(sd.Shutdown)
	s := &service{
		context: ctx,
		// Unbuffered, so that the order in which the events are sent is the
		// order in which they are received.
		events:       make(chan any),
		shutdown:     sd,
		initExitSent: make(map[*runc.Container]chan struct{}),
		cg2oom:       &fakeOOM{stop: func(string) {}},
	}
	p := process.New("c1", nil, stdio.Stdio{})
	p.Platform = fakePlatform{}
	return s, &runc.Container{ID: "c1", Bundle: t.TempDir()}, p
}

func receiveEvent(t *testing.T, s *service) any {
	t.Helper()
	select {
	case e := <-s.events:
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("no event was sent")
		return nil
	}
}

func TestSendDeleteAfterExit(t *testing.T) {
	s, c, p := newExitTestService(t)

	// Hold handleProcessExit after the process is marked as exited, before
	// the exit event is sent.
	release := make(chan struct{})
	s.cg2oom = &fakeOOM{stop: func(string) { <-release }}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		s.handleProcessExit(runcC.Exit{Pid: 1234, Status: 137}, c, p)
	}()
	require.NoError(t, p.Wait(context.Background()))

	// The process can now be deleted, but the delete event must wait for
	// the exit event.
	deleted := make(chan struct{})
	go func() {
		defer close(deleted)
		s.sendDelete(context.Background(), c, p)
	}()
	select {
	case e := <-s.events:
		t.Fatalf("%T sent before the exit event", e)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	exitedAt := protobuf.ToTimestamp(p.ExitedAt())
	require.Equal(t, &eventstypes.TaskExit{ContainerID: "c1", ID: "c1", Pid: 1234, ExitStatus: 137, ExitedAt: exitedAt}, receiveEvent(t, s))
	require.Equal(t, &eventstypes.TaskDelete{ContainerID: "c1", ExitStatus: 137, ExitedAt: exitedAt}, receiveEvent(t, s))
	<-exited
	<-deleted
	require.Empty(t, s.initExitSent)
}

func TestSendDeleteWithoutExit(t *testing.T) {
	// A process deleted before it was started never exits, and its delete
	// event must not wait for an exit event.
	s, c, p := newExitTestService(t)
	go s.sendDelete(context.Background(), c, p)
	require.IsType(t, &eventstypes.TaskDelete{}, receiveEvent(t, s))
}

func TestSendDeleteCanceled(t *testing.T) {
	s, c, p := newExitTestService(t)

	release := make(chan struct{})
	s.cg2oom = &fakeOOM{stop: func(string) { <-release }}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		s.handleProcessExit(runcC.Exit{Pid: 1234, Status: 137}, c, p)
	}()
	require.NoError(t, p.Wait(context.Background()))

	// A canceled Delete stops waiting for the exit event.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	go s.sendDelete(ctx, c, p)
	require.IsType(t, &eventstypes.TaskDelete{}, receiveEvent(t, s))

	close(release)
	require.IsType(t, &eventstypes.TaskExit{}, receiveEvent(t, s))
	<-exited
}

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
