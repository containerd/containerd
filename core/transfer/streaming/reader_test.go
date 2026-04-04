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
	"strings"
	"testing"
	"time"

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/typeurl/v2"
	"go.uber.org/goleak"
)

func TestReadByteStreamLeakScenarios(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "remote close",
			run: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				rs, ws := pipeStream()
				rbs := ReadByteStream(ctx, rs)

				ws.Close()

				buf := make([]byte, 10)
				if _, err := rbs.Read(buf); err == nil {
					t.Fatal("expected error after stream close")
				}
			},
		},
		{
			name: "context cancel before read",
			run: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				rs, _ := pipeStream()
				_ = ReadByteStream(ctx, rs)

				cancel()
				time.Sleep(10 * time.Millisecond)
			},
		},
		{
			name: "read blocked then remote close",
			run: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				rs, ws := pipeStream()
				rbs := ReadByteStream(ctx, rs)

				readDone := make(chan struct{})
				go func() {
					buf := make([]byte, 10)
					_, _ = rbs.Read(buf)
					close(readDone)
				}()

				ws.Close()

				select {
				case <-readDone:
				case <-time.After(time.Second):
					t.Fatal("read did not unblock after stream close")
				}
			},
		},
		{
			name: "close without parent cancel",
			run: func(t *testing.T) {
				rs, _ := pipeStream()
				rbs := ReadByteStream(context.Background(), rs)

				if err := rbs.Close(); err != nil {
					t.Fatalf("close read byte stream: %v", err)
				}
			},
		},
		{
			name: "write stream remote close",
			run: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				rs, ws := pipeStream()
				wbs := WriteByteStream(ctx, ws)

				rs.Close()

				if _, err := wbs.Write([]byte("foo")); err == nil {
					t.Fatal("expected error after stream close")
				}

				if err := wbs.Close(); err != nil {
					t.Fatalf("close write byte stream: %v", err)
				}
			},
		},
		{
			name: "send stream remote close without cancel",
			run: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				rs, ws := pipeStream()
				done := make(chan struct{})
				go func() {
					SendStream(ctx, strings.NewReader("payload"), ws)
					close(done)
				}()

				rs.Close()

				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("send stream did not stop after remote close")
				}
			},
		},
		{
			name: "send stream recv side close",
			run: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				rs, ws := pipeStream()
				r, w := io.Pipe()

				done := make(chan struct{})
				go func() {
					SendStream(ctx, r, ws)
					close(done)
				}()

				rs.Close()
				go func() {
					_, _ = w.Write([]byte("foo"))
					_ = w.Close()
				}()
				cancel()
				_ = r.CloseWithError(context.Canceled)

				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("send stream did not stop")
				}
			},
		},
		{
			name: "read byte stream propagates send failure",
			run: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				sendErr := errors.New("send window update failed")
				rbs := ReadByteStream(ctx, &failingSendStream{sendErr: sendErr})

				time.Sleep(10 * time.Millisecond)

				buf := make([]byte, 10)
				if _, err := rbs.Read(buf); !errors.Is(err, sendErr) {
					t.Fatalf("expected %v, got %v", sendErr, err)
				}
			},
		},
		{
			name: "write byte stream preserves partial write count on cause",
			run: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				rs, ws := pipeStream()
				wbs := WriteByteStream(ctx, ws)

				update := &transferapi.WindowUpdate{Update: maxRead}
				anyType, err := typeurl.MarshalAny(update)
				if err != nil {
					t.Fatalf("marshal window update: %v", err)
				}

				go func() {
					if err := rs.Send(anyType); err != nil {
						t.Errorf("send window update: %v", err)
						return
					}

					if _, err := rs.Recv(); err != nil {
						t.Errorf("recv data: %v", err)
						return
					}

					rs.Close()
				}()

				payload := make([]byte, maxRead+1)
				n, err := wbs.Write(payload)
				if n != maxRead {
					t.Fatalf("expected %d bytes written, got %d", maxRead, n)
				}
				if err == nil {
					t.Fatal("expected write error after remote close")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			tt.run(t)
		})
	}
}

type failingSendStream struct {
	sendErr error
}

func (f *failingSendStream) Send(typeurl.Any) error {
	return f.sendErr
}

func (f *failingSendStream) Recv() (typeurl.Any, error) {
	panic("unexpected Recv call")
}

func (f *failingSendStream) Close() error {
	return nil
}
