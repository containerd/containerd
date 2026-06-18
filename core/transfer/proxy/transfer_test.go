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
// method simulates the server behavior of sending a batch of progress events
// on the progress stream, closing the stream, and then returning from the RPC.
//
// This ordering (events sent + stream closed before the RPC returns) is exactly
// what the real server does (see plugins/services/transfer/service.go, which
// defers stream.Close). It is the worst case for the proxy consumer goroutine:
// when Transfer returns here, all events are already buffered and the stream is
// already closed, but the client-side Recv loop has not necessarily drained
// them yet. Without draining before returning, this reproduces the race that
// caused TestTransferImport/DigestRefs to be flaky (issue #10786).
type fakeTransferClient struct {
	// events is the set of progress events the "server" sends before returning.
	events []*transfertypes.Progress
	// returnErr, if non-nil, is returned from Transfer (error path).
	returnErr error
}

func (f *fakeTransferClient) Transfer(ctx context.Context, req *transferapi.TransferRequest) (*emptypb.Empty, error) {
	if req.Options != nil && req.Options.ProgressStream != "" {
		if stream, ok := streamForID(req.Options.ProgressStream); ok {
			for _, e := range f.events {
				a, err := typeurl.MarshalAny(e)
				if err != nil {
					return nil, err
				}
				if err := stream.Send(a); err != nil {
					return nil, err
				}
			}
			// Close the stream before returning, mirroring the server's
			// `defer stream.Close()`. The client Recv loop will then hit io.EOF
			// after draining the buffered events.
			stream.Close()
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
type fakeStream struct {
	mu     sync.Mutex
	queue  []typeurl.Any
	closed bool
}

func (s *fakeStream) Send(a typeurl.Any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	s.queue = append(s.queue, a)
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
		// Spin briefly until an event arrives or the stream is closed. A busy
		// wait is acceptable here because the test is tiny and the server side
		// (fakeTransferClient.Transfer) pushes events synchronously before
		// returning.
	}
}

func (s *fakeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// fakeStreamCreator hands out the fake stream registered for a given id.
type fakeStreamCreator struct {
	streams map[string]streaming.Stream
	mu      sync.Mutex
}

func (c *fakeStreamCreator) Create(_ context.Context, id string) (streaming.Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams == nil {
		c.streams = map[string]streaming.Stream{}
	}
	st := &fakeStream{}
	c.streams[id] = st
	return st, nil
}

// streamForID looks up the fake stream created for a given progress-stream id.
// Streams are registered by the creator and consumed by the fake client, so it
// must share the same registry. It is package-level to allow both to access it.
var (
	registryMu sync.Mutex
	registry   = map[string]streaming.Stream{}
)

func streamForID(id string) (streaming.Stream, bool) {
	registryMu.Lock()
	defer registryMu.Unlock()
	s, ok := registry[id]
	return s, ok
}

// newFakeTransferrer wires up a proxyTransferrer backed by fakes that share a
// registry of progress streams.
func newFakeTransferrer(events []*transfertypes.Progress, returnErr error) *proxyTransferrer {
	creator := &fakeStreamCreator{}
	return &proxyTransferrer{
		client: &fakeTransferClient{events: events, returnErr: returnErr},
		// Wrap the creator so each created stream is also registered for the
		// fake client to find by id.
		streamCreator: registeredCreator{creator},
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

type registeredCreator struct{ inner streaming.StreamCreator }

func (r registeredCreator) Create(ctx context.Context, id string) (streaming.Stream, error) {
	st, err := r.inner.Create(ctx, id)
	if err != nil {
		return nil, err
	}
	registryMu.Lock()
	registry[id] = st
	registryMu.Unlock()
	return st, nil
}

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
		mu      sync.Mutex
		got     []string
		resolve = make(chan struct{})
	)

	// progressKey collapses an event to a comparable string for assertions.
	progressKey := func(p transfer.Progress) string { return p.Event + "|" + p.Name }

	p := newFakeTransferrer(events, nil)
	err := p.Transfer(context.Background(), fakeMarshalable{}, fakeMarshalable{}, transfer.WithProgress(func(p transfer.Progress) {
		mu.Lock()
		got = append(got, progressKey(p))
		mu.Unlock()
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	close(resolve)

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
	progressKey := func(p transfer.Progress) string { return p.Event + "|" + p.Name }

	p := newFakeTransferrer(events, wantErr)
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
