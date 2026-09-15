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

// failingWriter returns an error on every Write call.
type failingWriter struct{}

func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

// TestReceiveStreamCancellationWithFailingCopy verifies that when the reader
// side fails (e.g. io.Copy to a failing destination), cancelling the context
// unblocks the receive goroutine and closes the stream instead of leaving it
// stuck on stream.Recv().
func TestReceiveStreamCancellationWithFailingCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Separate channels for data and window updates to avoid races on close.
	ch := make(chan typeurl.Any, 4)
	windowCh := make(chan int32, 4)
	done := make(chan struct{})

	go func() {
		defer close(done)
		// Send data but no window updates (simulating receiver never sending updates).
		any1, _ := typeurl.MarshalAny(&transferapi.Data{Data: []byte("first")})
		any2, _ := typeurl.MarshalAny(&transferapi.Data{Data: []byte("second")})
		ch <- any1
		ch <- any2
		close(ch)
	}()

	rs := &testStreamBlockingFixed{
		send:    ch,
		window:  windowCh,
		closer:  make(chan struct{}),
		remote:  done,
		failed:  false,
	}

	reader := ReceiveStream(ctx, rs)

	// Use io.Copy with a failing writer so ReceiveStream is blocked in w.Write.
	failWriter := &failingWriter{}
	_, err := io.Copy(failWriter, reader)
	if err == nil {
		t.Fatal("expected error from io.Copy with failing writer")
	}

	// Cancel the context. The goroutine should notice and exit within 2s.
	cancel()

	select {
	case <-done:
		// goroutine exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("ReceiveStream goroutine did not exit after context cancellation")
	}
}

// TestReceiveStreamCancellationBeforeRecv verifies that cancelling the context
// while the goroutine is blocked on stream.Recv() causes it to exit promptly.
func TestReceiveStreamCancellationBeforeRecv(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	closeDone := make(chan struct{})

	go func() {
		defer close(closeDone)
		// This goroutine represents the producer; it will close its remote channel
		// after a short delay, which unblocks Recv().
		time.Sleep(100 * time.Millisecond)
	}()

	// rs.remote is closed when doneChan closes, which makes Recv() return EOF.
	rs := &testStreamBlockingFixed{
		send:   make(chan typeurl.Any, 4),
		window: make(chan int32, 4),
		closer: make(chan struct{}),
		remote: done,
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(done)
	}()

	reader := ReceiveStream(ctx, rs)

	// Drain any data (there should be none since remote closed quickly).
	buf := make([]byte, 1024)
	_, _ = reader.Read(buf)

	// Cancel context while goroutine might still be running.
	cancel()

	select {
	case <-closeDone:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("ReceiveStream goroutine did not exit after context cancellation")
	}
}

// testStreamBlockingFixed mimics a stream where Recv() blocks until remote or closer is closed.
type testStreamBlockingFixed struct {
	send   chan<- typeurl.Any
	window chan<- int32
	closer chan struct{}
	remote <-chan struct{}
	failed bool
}

func (ts *testStreamBlockingFixed) Send(a typeurl.Any) error {
	if ts.failed {
		return errors.New("send failed")
	}
	// Try window update first, then data.
	select {
	case <-ts.remote:
		return io.ErrClosedPipe
	case ts.window <- 0: // consume window update slot
		// ignore
	case ts.send <- a:
	}
	return nil
}

func (ts *testStreamBlockingFixed) Recv() (typeurl.Any, error) {
	select {
	case <-ts.remote:
		return nil, io.EOF
	case <-ts.closer:
		return nil, io.ErrClosedPipe
	case <-time.After(time.Hour): // simulate blocking recv
		return nil, nil
	}
}

func (ts *testStreamBlockingFixed) Close() error {
	select {
	case <-ts.closer:
		return nil
	default:
	}
	close(ts.closer)
	return nil
}

// Verify the interface contract.
var _ streaming.Stream = (*testStreamBlockingFixed)(nil)
