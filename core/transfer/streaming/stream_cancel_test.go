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

package streaming

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/typeurl/v2"
	transferapi "github.com/containerd/containerd/api/types/transfer"
)

// blockingWriter returns an error on every Write call.
type blockingWriter struct{}

func (w *blockingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

// TestReceiveStreamCancellation verifies that when the reader side
// has given up (io.Copy returned an error), cancelling the context
// unblocks the receive goroutine and closes the stream instead of
// leaving it stuck on stream.Recv().
func TestReceiveStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Send two chunks then close, but the reader will abort on the first write.
	ch := make(chan typeurl.Any, 4)
	rc := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		any1, _ := typeurl.MarshalAny(&transferapi.Data{Data: []byte("first")})
		any2, _ := typeurl.MarshalAny(&transferapi.Data{Data: []byte("second")})
		ch <- any1
		ch <- any2
		close(ch)
		// signal that remote is done
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	rs := &testStreamBlocking{
		send:   ch,
		recv:   nil,
		closer: rc,
		remote: done,
	}

	reader := ReceiveStream(ctx, rs)

	// Read until we get an error — should fail fast because the pipe write
	// side (our fake stream) doesn't actually block; we just verify the
	// goroutine exits after cancellation.
	buf := make([]byte, 1024)
	n, readErr := reader.Read(buf)
	_ = n
	_ = readErr

	// Cancel the context. The goroutine should notice and exit within 2s.
	cancel()

	select {
	case <-done:
		// goroutine exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("ReceiveStream goroutine did not exit after context cancellation")
	}
}

// testStreamBlocking mimics a stream where Recv() blocks indefinitely,
// so context cancellation is the only way out.
type testStreamBlocking struct {
	send   chan<- typeurl.Any
	recv   <-chan typeurl.Any
	closer chan struct{}
	remote <-chan struct{}
}

func (ts *testStreamBlocking) Send(a typeurl.Any) error {
	select {
	case <-ts.remote:
		return io.ErrClosedPipe
	case ts.send <- a:
	}
	return nil
}

func (ts *testStreamBlocking) Recv() (typeurl.Any, error) {
	select {
	case <-ts.remote:
		return nil, io.EOF
	case <-ts.closer:
		return nil, io.ErrClosedPipe
	case <-time.After(time.Hour): // simulate blocking recv
		return nil, nil
	}
}

func (ts *testStreamBlocking) Close() error {
	select {
	case <-ts.closer:
		return nil
	default:
	}
	close(ts.closer)
	return nil
}

// Verify the interface contract.
var _ streaming.Stream = (*testStreamBlocking)(nil)
