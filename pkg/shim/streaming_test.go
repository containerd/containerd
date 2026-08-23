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

package shim

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/containerd/containerd/api/services/streaming/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/containerd/containerd/v2/core/streaming"
)

// fakeStream is an inert streaming.Stream for checking StreamManager bookkeeping.
type fakeStream struct {
	id     string
	closed *atomic.Bool
}

func (fakeStream) Send(typeurl.Any) error     { return nil }
func (fakeStream) Recv() (typeurl.Any, error) { return nil, nil }

func (f fakeStream) Close() error {
	if f.closed != nil {
		f.closed.Store(true)
	}
	return nil
}

// getStream returns the stream registered under id, failing rather than hanging if it
// never arrives.
func getStream(t *testing.T, m *StreamManager, id string) streaming.Stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := m.Get(ctx, id)
	require.NoError(t, err)
	return s
}

// TestStreamManagerGetBeforeRegister checks Get blocks until the stream is opened.
func TestStreamManagerGetBeforeRegister(t *testing.T) {
	m := NewStreamManager()
	want := fakeStream{id: "b"}

	type result struct {
		s   streaming.Stream
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		s, err := m.Get(context.Background(), "b")
		resCh <- result{s, err}
	}()

	// Get must still be blocked, since nothing has been registered.
	select {
	case r := <-resCh:
		t.Fatalf("Get returned early: %v %v", r.s, r.err)
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, m.Register("b", want))
	select {
	case r := <-resCh:
		require.NoError(t, r.err)
		assert.Equal(t, want, r.s)
	case <-time.After(10 * time.Second):
		t.Fatal("Get did not return after Register")
	}
}

func TestStreamManagerRegisterTwice(t *testing.T) {
	m := NewStreamManager()
	require.NoError(t, m.Register("c", fakeStream{id: "c"}))

	err := m.Register("c", fakeStream{id: "c2"})
	assert.ErrorIs(t, err, errdefs.ErrAlreadyExists)

	// The first registration wins.
	assert.Equal(t, fakeStream{id: "c"}, getStream(t, m, "c"))
}

// TestStreamManagerRegisterConcurrent checks racing registrations leave a single winner.
func TestStreamManagerRegisterConcurrent(t *testing.T) {
	const racers = 8
	m := NewStreamManager()

	start := make(chan struct{})
	errs := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- m.Register("f", fakeStream{id: "f"})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var won int
	for err := range errs {
		if err == nil {
			won++
			continue
		}
		assert.ErrorIs(t, err, errdefs.ErrAlreadyExists)
	}
	assert.Equal(t, 1, won)
}

func TestStreamManagerGetCancelled(t *testing.T) {
	m := NewStreamManager()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.Get(ctx, "d")
	assert.ErrorIs(t, err, context.Canceled)

	// The abandoned entry is dropped, so a later Register succeeds.
	require.NoError(t, m.Register("d", fakeStream{id: "d"}))
}

func TestStreamManagerDelete(t *testing.T) {
	m := NewStreamManager()
	require.NoError(t, m.Register("e", fakeStream{id: "e"}))
	m.Delete("e")

	// Deleting forgets the registration, so the ID can be reused.
	require.NoError(t, m.Register("e", fakeStream{id: "e2"}))
	assert.Equal(t, fakeStream{id: "e2"}, getStream(t, m, "e"))
}

// fakeStreamServer feeds ttrpcStreamingService.Stream a single StreamInit.
type fakeStreamServer struct{ init typeurl.Any }

func (f *fakeStreamServer) Recv() (*anypb.Any, error) {
	if f.init == nil {
		return nil, io.EOF
	}
	a := typeurl.MarshalProto(f.init)
	f.init = nil
	return a, nil
}

func (f *fakeStreamServer) Send(*anypb.Any) error     { return nil }
func (f *fakeStreamServer) SendMsg(interface{}) error { return nil }
func (f *fakeStreamServer) RecvMsg(interface{}) error { return nil }

// TestStreamEndDropsEntry checks an ended stream is removed, so abandoned streams do not
// accumulate.
func TestStreamEndDropsEntry(t *testing.T) {
	m := NewStreamManager()
	svc := &ttrpcStreamingService{manager: m}

	init, err := typeurl.MarshalAny(&api.StreamInit{ID: "g"})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Stream(ctx, &fakeStreamServer{init: init}) }()

	// Get returns once the stream is registered.
	registered := getStream(t, m, "g")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Stream did not return after cancellation")
	}

	m.mu.Lock()
	_, present := m.entries["g"]
	m.mu.Unlock()
	assert.False(t, present, "the entry should be dropped once the stream ends")

	// A stream that ended must not evict whoever registered the ID next.
	require.NoError(t, m.Register("g", fakeStream{id: "g2"}))
	m.deleteStream("g", registered)
	assert.Equal(t, fakeStream{id: "g2"}, getStream(t, m, "g"))
}

// TestOpenPortForwardMissingOutStream checks the input stream is closed and released when
// the output stream never arrives.
func TestOpenPortForwardMissingOutStream(t *testing.T) {
	m := NewStreamManager()
	var closed atomic.Bool
	require.NoError(t, m.Register("pf-in", fakeStream{id: "pf-in", closed: &closed}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := m.OpenPortForward(ctx, "pf")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output stream")
	assert.True(t, closed.Load(), "the input stream should be closed")

	// The input stream is no longer registered, so the ID can be reused.
	require.NoError(t, m.Register("pf-in", fakeStream{id: "pf-in2"}))
}
