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
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/containerd/containerd/pkg/streaming"
	"github.com/containerd/typeurl/v2"
)

func FuzzSendAndReceive(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add(bytes.Repeat([]byte{0}, windowSize+1))
	f.Add([]byte("hello"))
	f.Add(bytes.Repeat([]byte("hello"), windowSize+1))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Fuzz(func(t *testing.T, expected []byte) {
		runSendAndReceiveFuzz(ctx, t, expected)
		runSendAndReceiveChainFuzz(ctx, t, expected)
		runWriterFuzz(ctx, t, expected)
	})
}

func runSendAndReceiveFuzz(ctx context.Context, t *testing.T, expected []byte) {
	rs, ws := pipeStream()
	r, w := io.Pipe()
	SendStream(ctx, r, ws)
	or := ReceiveStream(ctx, rs)

	go func() {
		io.Copy(w, bytes.NewBuffer(expected))
		w.Close()
	}()

	actual, err := io.ReadAll(or)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expected, actual) {
		t.Fatalf("received bytes are not equal\n\tactual: %v\n\texpected:%v", actual, expected)
	}
}

func runSendAndReceiveChainFuzz(ctx context.Context, t *testing.T, expected []byte) {
	r, w := io.Pipe()

	or := chainStreams(ctx, chainStreams(ctx, chainStreams(ctx, r)))

	go func() {
		io.Copy(w, bytes.NewBuffer(expected))
		w.Close()
	}()

	actual, err := io.ReadAll(or)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expected, actual) {
		t.Fatalf("received bytes are not equal\n\tactual: %v\n\texpected:%v", actual, expected)
	}
}

func runWriterFuzz(ctx context.Context, t *testing.T, expected []byte) {
	rs, ws := pipeStream()
	wc := WriteByteStream(ctx, ws)
	or := ReceiveStream(ctx, rs)

	go func() {
		io.Copy(wc, bytes.NewBuffer(expected))
		wc.Close()
	}()

	actual, err := io.ReadAll(or)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expected, actual) {
		t.Fatalf("received bytes are not equal\n\tactual: %v\n\texpected:%v", actual, expected)
	}
}

func chainStreams(ctx context.Context, r io.Reader) io.Reader {
	rs, ws := pipeStream()
	SendStream(ctx, r, ws)
	return ReceiveStream(ctx, rs)
}

func pipeStream() (streaming.Stream, streaming.Stream) {
	r := make(chan typeurl.Any)
	rc := make(chan struct{})
	w := make(chan typeurl.Any)
	wc := make(chan struct{})

	rs := &testStream{
		send:   w,
		recv:   r,
		closer: wc,
		remote: rc,
	}
	ws := &testStream{
		send:   r,
		recv:   w,
		closer: rc,
		remote: wc,
	}
	return rs, ws
}

type testStream struct {
	send   chan<- typeurl.Any
	recv   <-chan typeurl.Any
	closer chan struct{}
	remote <-chan struct{}
}

func (ts *testStream) Send(a typeurl.Any) error {
	select {
	case <-ts.remote:
		return io.ErrClosedPipe
	case ts.send <- a:
	}
	return nil
}
func (ts *testStream) Recv() (typeurl.Any, error) {
	select {
	case <-ts.remote:
		return nil, io.EOF
	case a := <-ts.recv:
		return a, nil
	}
}

func (ts *testStream) Close() error {
	select {
	case <-ts.closer:
		return nil
	default:
	}
	close(ts.closer)
	return nil
}
