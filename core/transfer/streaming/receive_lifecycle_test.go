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
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/containerd/v2/core/transfer/archive"
	"github.com/containerd/containerd/v2/core/transfer/registry"
	tstreaming "github.com/containerd/containerd/v2/core/transfer/streaming"
	"github.com/containerd/typeurl/v2"
)

func TestReceiveStreamCancellation(t *testing.T) {
	for _, consumerStopped := range []bool{false, true} {
		name := "blocked pipe write"
		if consumerStopped {
			name = "destination failure"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				stream := &receiveTestStream{ctx: ctx, data: [][]byte{[]byte("first"), []byte("second")}}
				reader := tstreaming.ReceiveStream(ctx, stream)
				if consumerStopped {
					_, err := io.Copy(&receiveTestDestination{err: errReceiveDestination}, reader)
					if !errors.Is(err, errReceiveDestination) {
						t.Fatal(err)
					}
				}
				synctest.Wait() // The receiver is blocked in io.Pipe.Write.
				cancel()
				synctest.Wait()
				if stream.closes != 1 {
					t.Errorf("stream closed %d times after cancellation, want 1", stream.closes)
				}
				_, err := reader.Read(make([]byte, 1))
				if !errors.Is(err, context.Canceled) {
					t.Errorf("Read error = %v, want context.Canceled", err)
				}
				// Release the original implementation too, so a failure does not leak a goroutine.
				reader.(io.Closer).Close()
				synctest.Wait()
			})
		})
	}
}

func TestReceiveStreamCompletion(t *testing.T) {
	for _, receiveErr := range []error{io.EOF, io.ErrUnexpectedEOF} {
		t.Run(receiveErr.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				stream := &receiveTestStream{ctx: ctx, data: [][]byte{[]byte("first"), []byte("second")}, receiveErr: receiveErr}
				reader := tstreaming.ReceiveStream(ctx, stream)
				data, err := io.ReadAll(reader)
				if string(data) != "firstsecond" {
					t.Fatalf("data = %q", data)
				}
				if receiveErr == io.EOF {
					if err != nil {
						t.Fatal(err)
					}
				} else if !errors.Is(err, receiveErr) {
					t.Errorf("ReadAll error = %v, want %v", err, receiveErr)
				}
				synctest.Wait()
				cancel() // Completed reads retain their terminal error after later cancellation.
				synctest.Wait()
				if stream.closes != 1 {
					t.Errorf("stream closed %d times, want 1", stream.closes)
				}
				_, err = reader.Read(make([]byte, 1))
				if !errors.Is(err, receiveErr) {
					t.Errorf("terminal Read error = %v, want %v", err, receiveErr)
				}
			})
		})
	}
}

func TestReceiveStreamCanceledContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		stream := &receiveTestStream{ctx: ctx}
		_, err := io.ReadAll(tstreaming.ReceiveStream(ctx, stream))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("ReadAll error = %v, want context.Canceled", err)
		}
		synctest.Wait()
		if stream.closes != 1 {
			t.Errorf("stream closed %d times, want 1", stream.closes)
		}
	})
}

func TestReceiveStreamCopyLifecycle(t *testing.T) {
	marshals := []struct {
		name    string
		marshal func(context.Context, streaming.StreamCreator, io.WriteCloser) error
	}{
		{"export", func(ctx context.Context, sm streaming.StreamCreator, w io.WriteCloser) error {
			_, err := archive.NewImageExportStream(w, "application/x-tar").MarshalAny(ctx, sm)
			return err
		}},
		{"http debug", func(ctx context.Context, sm streaming.StreamCreator, w io.WriteCloser) error {
			r, err := registry.NewOCIRegistry(ctx, "example.com/test:latest", registry.WithHTTPDebug(), registry.WithClientStream(w))
			if err != nil {
				return err
			}
			_, err = r.MarshalAny(ctx, sm)
			return err
		}},
	}
	for _, m := range marshals {
		for _, tc := range []struct {
			name       string
			writeErr   error
			receiveErr error
			createErr  error
		}{
			{name: "destination failure", writeErr: errReceiveDestination},
			{name: "EOF", receiveErr: io.EOF},
			{name: "receive failure", receiveErr: io.ErrUnexpectedEOF},
			{name: "create failure", createErr: errors.New("cannot create stream")},
		} {
			t.Run(m.name+"/"+tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					stream := &receiveTestStream{data: [][]byte{[]byte("payload")}, receiveErr: tc.receiveErr}
					creator := &receiveTestCreator{stream: stream, err: tc.createErr}
					dest := &receiveTestDestination{err: tc.writeErr}
					err := m.marshal(ctx, creator, dest)
					if !errors.Is(err, tc.createErr) {
						t.Fatalf("MarshalAny error = %v, want %v", err, tc.createErr)
					}
					synctest.Wait()
					if !errors.Is(creator.ctx.Err(), context.Canceled) {
						t.Error("stream context not canceled after copy or creation failure")
					}
					if ctx.Err() != nil {
						t.Errorf("parent context canceled: %v", ctx.Err())
					}
					if tc.createErr == nil {
						if dest.closes != 1 {
							t.Errorf("destination closed %d times, want 1", dest.closes)
						}
						if stream.closes != 1 {
							t.Errorf("stream closed %d times, want 1", stream.closes)
						}
						if tc.writeErr == nil && dest.String() != "payload" {
							t.Errorf("output = %q", dest.String())
						}
					}
					// Unblock Recv on the original implementation after recording the failure.
					cancel()
					synctest.Wait()
				})
			})
		}
	}
}

var errReceiveDestination = errors.New("destination write failed")

type receiveTestDestination struct {
	bytes.Buffer
	err    error
	closes int
}

func (w *receiveTestDestination) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	return w.Buffer.Write(p)
}

// Hide bytes.Buffer.ReadFrom so io.Copy exercises Write, including its error.
func (w *receiveTestDestination) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{w}, r)
}
func (w *receiveTestDestination) Close() error { w.closes++; return nil }

type receiveTestStream struct {
	ctx        context.Context
	data       [][]byte
	receiveErr error
	closes     int
}

func (s *receiveTestStream) Send(typeurl.Any) error { return nil }
func (s *receiveTestStream) Recv() (typeurl.Any, error) {
	if len(s.data) > 0 {
		data := s.data[0]
		s.data = s.data[1:]
		return typeurl.MarshalAny(&transferapi.Data{Data: data})
	}
	if s.receiveErr != nil {
		return nil, s.receiveErr
	}
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
func (s *receiveTestStream) Close() error { s.closes++; return nil }

type receiveTestCreator struct {
	stream *receiveTestStream
	ctx    context.Context
	err    error
}

func (s *receiveTestCreator) Create(ctx context.Context, _ string) (streaming.Stream, error) {
	s.ctx = ctx
	s.stream.ctx = ctx
	if s.err != nil {
		return nil, s.err
	}
	return s.stream, nil
}
