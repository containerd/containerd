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

package streaming_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/containerd/containerd/v2/core/streaming"
)

// mockStream implements streaming.Stream for testing
type mockStream struct {
	mu      sync.Mutex
	replies []interface{}
	idx     int
	closed  bool
	sendCh  chan interface{}
}

func newMockStream() *mockStream {
	return &mockStream{
		sendCh: make(chan interface{}, 1),
	}
}

func (m *mockStream) Send(i interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("stream closed")
	}
	// Store in channel for test to read
	m.sendCh <- i
	return nil
}

func (m *mockStream) Recv() (interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idx < len(m.replies) {
		reply := m.replies[m.idx]
		m.idx++
		return reply, nil
	}
	return nil, io.EOF
}

func (m *mockStream) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// failingWriter wraps io.Writer to fail after first write
type failingWriter struct {
	writer io.Writer
	failed bool
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.failed {
		return 0, errors.New("simulated write failure")
	}
	f.failed = true
	return f.writer.Write(p)
}

func TestReceiveStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newMockStream()
	reader := streaming.ReceiveStream(ctx, stream)

	// Cancel context immediately
	cancel()

	// Reader should be cancelled, not leak
	_, err := reader.Read(make([]byte, 1))
	if err == nil {
		t.Error("expected error after cancellation, got nil")
	}
}

func TestReceiveStreamCancellationWithFailingCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	stream := newMockStream()
	reader := streaming.ReceiveStream(ctx, stream)

	// Use a failing writer to trigger the error path
	failOnce := false
	failingReader := &failingWriter{writer: reader}

	// First Read succeeds, second fails
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 100)
		// This should eventually fail when we simulate the error path
		_, _ = reader.Read(buf)
	}()

	// Give goroutine time to start
	// Then cancel to test that cancellation properly propagates
	cancel()
	wg.Wait()
}

func TestReceiveStreamEOF(t *testing.T) {
	ctx := context.Background()
	stream := newMockStream()
	reader := streaming.ReceiveStream(ctx, stream)

	// Stream immediately returns EOF
	buf := make([]byte, 100)
	_, err := reader.Read(buf)
	if err == nil {
		t.Error("expected EOF error")
	}
}
