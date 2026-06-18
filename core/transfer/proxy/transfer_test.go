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

package proxy

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	transferapi "github.com/containerd/containerd/api/services/transfer/v1"
	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/containerd/v2/core/transfer"
	"github.com/containerd/typeurl/v2"
)

// fakeTransferClient implements transferapi.TTRPCTransferService. Its Transfer
// method simulates the server: it returns the RPC response while (on a separate
// goroutine) pushing progress events into the stream and then closing it.
//
// The real server pushes events synchronously during importStream and closes
// the stream via `defer stream.Close()` right before the RPC returns. On the
// wire these are independent multiplexed streams, which is exactly what makes
// the proxy's missing join a *flaky* (timing-dependent) bug. To make this
// regression test deterministic, the fake pushes events from a background
// goroutine with a small delay between them, widening the window so that —
// without the drain — the caller deterministically observes fewer events than
// were sent. With the drain, Transfer blocks until the consumer has dispatched
// every event and the stream is closed, so the assertion always holds.
type fakeTransferClient struct {
	// streams maps progress-stream ids to the streams created by the paired
	// creator. Scoped to this fake transferrer (not package-level) so tests
	// don't pollute each other.
	streams map[string]streaming.Stream
	mu      sync.Mutex
	// events is the set of progress events the "server" sends.
	events []*transfertypes.Progress
	// returnErr, if non-nil, is returned from Transfer (error path).
	returnErr error
	// sendDelay is the delay between sending events, widening the race window
	// for deterministic reproduction.
	sendDelay time.Duration
}

func (f *fakeTransferClient) Transfer(ctx context.Context, req *transferapi.TransferRequest) (*emptypb.Empty, error) {
	if req.Options != nil && req.Options.ProgressStream != "" {
		f.mu.Lock()
		stream, ok := f.streams[req.Options.ProgressStream]
		f.mu.Unlock()
		if ok {
			// Push events and close the stream from a background goroutine so
			// the RPC can return immediately, mirroring how the real server's
			// stream activity and RPC response are independent on the wire.
			go func() {
				for _, e := range f.events {
					if f.sendDelay > 0 {
						time.Sleep(f.sendDelay)
					}
					a, err := typeurl.MarshalAny(e)
					if err != nil {
						return
					}
					if err := stream.Send(a); err != nil {
						return
					}
				}
				stream.Close()
			}()
		}
	}
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return &emptypb.Empty{}, nil
}

// fakeStream is a minimal in-memory streaming.Stream used to model a single
// progress stream. Send enqueues objects that Recv later returns; once Close is
// called and the queue is drained, Recv returns io.EOF.
//
// A notification channel (ready) is signaled by Send/Close and waited on by
// Recv, so Recv blocks (rather than busy-loops) when there is nothing to do.
type fakeStream struct {
	mu     sync.Mutex
	queue  []typeurl.Any
	closed bool
	ready  chan struct{}
}

func newFakeStream() *fakeStream {
	return &fakeStream{ready: make(chan struct{}, 1)}
}

// signal wakes a blocked Recv, if any. Non-blocking: the channel is buffered
// with capacity 1 so a pending signal is coalesced when Recv isn't waiting.
func (s *fakeStream) signal() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *fakeStream) Send(a typeurl.Any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	s.queue = append(s.queue, a)
	s.signal()
	return nil
}

func (s *fakeStream) Recv() (typeurl.Any, error) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			a := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return a, nil
		}
		if s.closed {
			s.mu.Unlock()
			return nil, io.EOF
		}
		s.mu.Unlock()
		// Block until Send/Close signals, instead of spinning.
		<-s.ready
	}
}

func (s *fakeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.signal()
	return nil
}

// fakeStreamCreator creates fake streams and registers them with the paired
// fake client so the server side (fakeTransferClient.Transfer) can look them up
// by progress-stream id.
type fakeStreamCreator struct {
	client *fakeTransferClient
}

func (c *fakeStreamCreator) Create(_ context.Context, id string) (streaming.Stream, error) {
	st := newFakeStream()
	c.client.mu.Lock()
	if c.client.streams == nil {
		c.client.streams = map[string]streaming.Stream{}
	}
	c.client.streams[id] = st
	c.client.mu.Unlock()
	return st, nil
}

// newFakeTransferrer wires up a proxyTransferrer backed by fakes that share a
// stream registry scoped to this instance only (no package-level state).
// sendDelay widens the race window between event send and stream close so the
// missing-drain bug reproduces deterministically.
func newFakeTransferrer(events []*transfertypes.Progress, returnErr error, sendDelay time.Duration) *proxyTransferrer {
	client := &fakeTransferClient{events: events, returnErr: returnErr, sendDelay: sendDelay}
	return &proxyTransferrer{
		client:        client,
		streamCreator: &fakeStreamCreator{client: client},
	}
}

// fakeMarshalable is a stand-in source/destination for Transfer that satisfies
// proxyTransferrer's internal streamMarshaler interface, so Transfer can
// marshal it into a request without depending on typeurl registration. Its
// marshaled form is opaque; the fake client ignores the request payload and
// only cares about the progress stream id.
type fakeMarshalable struct{}

func (fakeMarshalable) MarshalAny(context.Context, streaming.StreamCreator) (typeurl.Any, error) {
	return &anyStub{typeURL: "fake.test/marshalable", value: []byte("{}")}, nil
}

type anyStub struct {
	typeURL string
	value   []byte
}

func (a *anyStub) GetTypeUrl() string { return a.typeURL }
func (a *anyStub) GetValue() []byte   { return a.value }

// progressKey collapses an event to a comparable string for assertions.
func progressKey(p transfer.Progress) string { return p.Event + "|" + p.Name }

// TestTransferProgressDrainedOnReturn verifies that, after Transfer returns,
// every progress event the server sent before returning has been delivered to
// the caller's Progress callback. This is the regression test for the flaky
// TestTransferImport/DigestRefs (issue #10786): without draining the progress
// stream consumer before returning, the final events could be observed as
// missing.
func TestTransferProgressDrainedOnReturn(t *testing.T) {
	events := []*transfertypes.Progress{
		{Event: "Importing"},
		{Event: "saved", Name: "registry.test/all-refs:index"},
		{Event: "saved", Name: "registry.test/all-refs:1"},
		{Event: "saved", Name: "registry.test/all-refs@sha256:manifest"},
		{Event: "Completed import"},
	}

	var (
		mu  sync.Mutex
		got []string
	)

	p := newFakeTransferrer(events, nil, 5*time.Millisecond)
	err := p.Transfer(context.Background(), fakeMarshalable{}, fakeMarshalable{}, transfer.WithProgress(func(p transfer.Progress) {
		mu.Lock()
		got = append(got, progressKey(p))
		mu.Unlock()
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(events) {
		t.Fatalf("expected %d progress events after Transfer returned, got %d: %v\n"+
			"This indicates the proxy returned before the progress stream was drained (see issue #10786).",
			len(events), len(got), got)
	}
	want := make(map[string]int, len(events))
	for _, e := range events {
		want[progressKey(transfer.Progress{Event: e.Event, Name: e.Name})]++
	}
	for _, g := range got {
		want[g]--
		if want[g] < 0 {
			t.Fatalf("unexpected progress event %q (remaining map: %v)", g, want)
		}
	}
}

// TestTransferProgressDrainedOnError verifies that progress events are also
// drained when the underlying Transfer RPC returns an error, so delivery
// semantics are consistent across success and failure paths.
func TestTransferProgressDrainedOnError(t *testing.T) {
	events := []*transfertypes.Progress{
		{Event: "Importing"},
		{Event: "saved", Name: "registry.test/partial:1"},
	}
	wantErr := errors.New("boom")

	var (
		mu  sync.Mutex
		got []string
	)

	p := newFakeTransferrer(events, wantErr, 5*time.Millisecond)
	err := p.Transfer(context.Background(), fakeMarshalable{}, fakeMarshalable{}, transfer.WithProgress(func(p transfer.Progress) {
		mu.Lock()
		got = append(got, progressKey(p))
		mu.Unlock()
	}))
	if err == nil {
		t.Fatalf("expected error from Transfer, got nil")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(events) {
		t.Fatalf("expected %d progress events after errored Transfer returned, got %d: %v",
			len(events), len(got), got)
	}
}
