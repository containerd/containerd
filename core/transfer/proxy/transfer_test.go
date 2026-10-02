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

	"google.golang.org/protobuf/types/known/emptypb"

	transferapi "github.com/containerd/containerd/api/services/transfer/v1"
	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/containerd/v2/core/transfer"
	"github.com/containerd/typeurl/v2"
)

// fakeTransferClient implements transferapi.TTRPCTransferService. Its Transfer
// method simulates the server: it returns the RPC response immediately while a
// background goroutine pushes progress events into the stream and closes it.
//
// The real server's stream activity (progress events) and the RPC response
// travel over independent multiplexed streams, so the RPC can return before the
// client has drained the progress stream — that independence is exactly what
// makes the proxy's missing join a real race.
//
// To make the regression test fully deterministic (no sleeps, no timing), the
// fake exposes two synchronization channels:
//   - gate: the background sender blocks on <-gate before sending any event, so
//     the test controls exactly when events start flowing.
//   - sent: closed after all events have been pushed and the stream closed, so
//     the test can know the server side is fully done.
//
// With the drain (the fix), proxyTransferrer.Transfer cannot return until the
// stream is closed (and `sent` is closed right after, in sendEvents), so the
// test observes all events. Without the drain, Transfer returns the moment the
// RPC returns — before gate is even opened — so the test observes zero events.
type fakeTransferClient struct {
	// streams maps progress-stream ids to the streams created by the paired
	// creator. Scoped to this fake transferrer (not package-level) so tests
	// don't pollute each other.
	streams map[string]streaming.Stream
	mu      sync.Mutex

	events    []*transfertypes.Progress
	returnErr error

	// gate is closed by the test to allow the background sender to start
	// pushing events. nil when no gating is desired (sender sends eagerly).
	gate chan struct{}
	// sent is closed once all events have been pushed and the stream closed.
	sent chan struct{}
}

func (f *fakeTransferClient) Transfer(ctx context.Context, req *transferapi.TransferRequest) (*emptypb.Empty, error) {
	if req.Options != nil && req.Options.ProgressStream != "" {
		f.mu.Lock()
		stream, ok := f.streams[req.Options.ProgressStream]
		f.mu.Unlock()
		if ok {
			go f.sendEvents(stream)
		}
	}
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return &emptypb.Empty{}, nil
}

// sendEvents pushes all events into the stream and closes it. It blocks on gate
// first so the test can stage the "RPC returned, events not yet sent" window.
func (f *fakeTransferClient) sendEvents(stream streaming.Stream) {
	defer close(f.sent)
	if f.gate != nil {
		<-f.gate
	}
	for _, e := range f.events {
		a, err := typeurl.MarshalAny(e)
		if err != nil {
			return
		}
		if err := stream.Send(a); err != nil {
			return
		}
	}
	stream.Close()
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
//
// If gate is non-nil, the server's progress sender blocks on <-gate before
// pushing any event, letting the test stage a deterministic "RPC returned,
// events not yet sent" window (used by the success/error drain tests). If gate
// is nil, the server sends events eagerly (used by the consumer-ordering test,
// which synchronizes from the consumer side instead).
func newFakeTransferrer(events []*transfertypes.Progress, returnErr error, gate chan struct{}) (transferrer *proxyTransferrer, sent chan struct{}) {
	sent = make(chan struct{})
	client := &fakeTransferClient{events: events, returnErr: returnErr, gate: gate, sent: sent}
	return &proxyTransferrer{
		client:        client,
		streamCreator: &fakeStreamCreator{client: client},
	}, sent
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

// assertProgress asserts that the collected events match the expected set
// (order-independent). It is a helper used by the tests below.
func assertProgress(t *testing.T, got []string, events []*transfertypes.Progress) {
	t.Helper()
	if len(got) != len(events) {
		t.Fatalf("expected %d progress events, got %d: %v\n"+
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

// TestTransferProgressDrainedOnReturn verifies that proxyTransferrer.Transfer
// does not return until every progress event has been delivered to the caller's
// Progress callback. This is the regression test for the flaky
// TestTransferImport/DigestRefs (issue #10786).
//
// Determinism: the fake server's progress sender blocks on `gate` until the
// test opens it. The fake RPC returns immediately. So with the drain in place,
// Transfer blocks until the test opens gate (events flow, stream closes); the
// test only reads `got` after Transfer returns, by which point all events are
// guaranteed delivered. Run against the pre-fix transfer.go, Transfer returns
// while events are still gated off, so `got` is empty — the bug reproduces
// every run, with no sleeps.
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

	gate := make(chan struct{})
	p, sent := newFakeTransferrer(events, nil, gate)

	transferDone := make(chan error, 1)
	go func() {
		transferDone <- p.Transfer(context.Background(), fakeMarshalable{}, fakeMarshalable{}, transfer.WithProgress(func(p transfer.Progress) {
			mu.Lock()
			got = append(got, progressKey(p))
			mu.Unlock()
		}))
	}()

	// Release the server-side progress events now. With the fix, Transfer is
	// blocked on the drain and will only return after the stream closes (i.e.
	// after `sent` fires). Without the fix, Transfer may already have returned.
	close(gate)
	<-sent

	// Once the server has closed the stream, the drained Transfer must return.
	if err := <-transferDone; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	assertProgress(t, got, events)
}

// TestTransferProgressDrainedOnError verifies that progress events are also
// drained when the underlying Transfer RPC returns an error, so delivery
// semantics are consistent across success and failure paths. Same gated
// determinism as the success test.
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

	gate := make(chan struct{})
	p, sent := newFakeTransferrer(events, wantErr, gate)

	transferDone := make(chan error, 1)
	go func() {
		transferDone <- p.Transfer(context.Background(), fakeMarshalable{}, fakeMarshalable{}, transfer.WithProgress(func(p transfer.Progress) {
			mu.Lock()
			got = append(got, progressKey(p))
			mu.Unlock()
		}))
	}()

	close(gate)
	<-sent

	err := <-transferDone
	if err == nil {
		t.Fatalf("expected error from Transfer, got nil")
	}

	mu.Lock()
	defer mu.Unlock()
	assertProgress(t, got, events)
}

// TestTransferWaitsForConsumer verifies the ordering half of the drain
// contract: Transfer must not return until the progress consumer goroutine has
// finished dispatching every event. The other two tests prove the "eventual
// state" (all events are observed after Transfer returns); this one proves the
// "ordering" (Transfer's return is gated on the consumer completing).
//
// It does so by making the Progress callback block on the last event. While the
// callback is blocked, the consumer goroutine is still alive, so the fix's
// done-channel cannot be closed and Transfer must still be blocked in its drain
// wait. The test asserts Transfer is blocked at that point, then releases the
// callback and observes Transfer returning.
//
// This is the closest deterministic encoding of the original flake: the real
// server had already sent everything, but the client consumer hadn't finished
// draining when Transfer (incorrectly) returned.
//
// Determinism note: no sleep is used. The non-blocking select on transferDone
// is safe without a grace period because the fake RPC returns immediately, so
// any non-draining implementation would have written transferDone well before
// the consumer reaches the blocking callback (which requires multiple channel
// round-trips through the stream).
func TestTransferWaitsForConsumer(t *testing.T) {
	events := []*transfertypes.Progress{
		{Event: "saved", Name: "registry.test/a:1"},
		{Event: "Completed import"},
	}

	var (
		mu              sync.Mutex
		got             []string
		reachedLast     = make(chan struct{})
		progressRelease = make(chan struct{})
	)

	p, _ := newFakeTransferrer(events, nil, nil)
	// No gate here: the server sends events immediately, which is the realistic
	// case the original bug occurred under. The synchronization comes from the
	// consumer side (reachedLast / progressRelease), not the server side.

	transferDone := make(chan error, 1)
	go func() {
		transferDone <- p.Transfer(context.Background(), fakeMarshalable{}, fakeMarshalable{}, transfer.WithProgress(func(p transfer.Progress) {
			mu.Lock()
			got = append(got, progressKey(p))
			last := len(got) == len(events)
			mu.Unlock()

			if last {
				// Tell the test we've reached the final event and are about to
				// block, then block until the test releases us. While blocked,
				// the consumer goroutine (and hence the drain's done channel)
				// cannot complete.
				close(reachedLast)
				<-progressRelease
			}
		}))
	}()

	// Wait until the consumer has dispatched the final event and is now blocked
	// in the callback. At this point a draining Transfer must still be waiting.
	<-reachedLast

	select {
	case err := <-transferDone:
		t.Fatalf("Transfer returned before the progress consumer finished draining (err=%v); "+
			"this is the #10786 race — Transfer must block until the consumer completes", err)
	default:
		// Good: Transfer is still blocked in its drain wait, exactly as the
		// contract requires.
	}

	// Release the blocked callback so the consumer can finish and Transfer can
	// return.
	close(progressRelease)

	if err := <-transferDone; err != nil {
		t.Fatalf("unexpected error after releasing consumer: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	assertProgress(t, got, events)
}
