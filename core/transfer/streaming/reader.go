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
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/typeurl/v2"
)

type readByteStream struct {
	ctx       context.Context
	cancel    context.CancelCauseFunc
	stream    streaming.Stream
	closeOnce sync.Once
	window    atomic.Int32
	updated   chan struct{}
	remaining []byte
}

func ReadByteStream(ctx context.Context, stream streaming.Stream) io.ReadCloser {
	ctx, cancel := context.WithCancelCause(ctx)
	rbs := &readByteStream{
		ctx:     ctx,
		cancel:  cancel,
		stream:  stream,
		updated: make(chan struct{}, 1),
	}
	context.AfterFunc(rbs.ctx, func() { _ = rbs.closeStream() })
	go func() {
		for {
			select {
			case <-rbs.ctx.Done():
				return
			default:
			}
			if rbs.window.Load() >= windowSize {
				select {
				case <-rbs.ctx.Done():
					return
				case <-rbs.updated:
					continue
				}
			}
			update := &transferapi.WindowUpdate{
				Update: windowSize,
			}
			anyType, err := typeurl.MarshalAny(update)
			if err != nil {
				rbs.cancel(err)
				return
			}
			if err := stream.Send(anyType); err != nil {
				if !errors.Is(err, io.EOF) {
					rbs.cancel(err)
					return
				}
				return
			}
			rbs.window.Add(windowSize)
		}

	}()
	return rbs
}

func (r *readByteStream) Read(p []byte) (n int, err error) {
	plen := len(p)
	if len(r.remaining) > 0 {
		copied := copy(p, r.remaining)
		if len(r.remaining) > plen {
			r.remaining = r.remaining[plen:]
		} else {
			r.remaining = nil
		}
		return copied, nil
	}

	select {
	case <-r.ctx.Done():
		if err := context.Cause(r.ctx); err != nil {
			return 0, err
		}
		return 0, r.ctx.Err()
	default:
	}
	anyType, err := r.stream.Recv()
	if err != nil {
		if cause := context.Cause(r.ctx); cause != nil && (errors.Is(err, context.Canceled) || errors.Is(err, io.EOF)) {
			return 0, cause
		}
		return 0, err
	}
	i, err := typeurl.UnmarshalAny(anyType)
	if err != nil {
		return 0, fmt.Errorf("failed to unmarshal received object: %w", err)
	}
	switch v := i.(type) {
	case *transferapi.Data:
		n = copy(p, v.Data)
		if len(v.Data) > plen {
			r.remaining = v.Data[plen:]
		}

		if r.window.Add(-int32(n)) < windowSize {
			select {
			case r.updated <- struct{}{}:
			default:
			}
		}
		return n, nil
	default:
		return 0, fmt.Errorf("stream received error type %v", v)
	}
}

func (r *readByteStream) Close() error {
	if r.cancel != nil {
		r.cancel(nil)
	}
	return r.closeStream()
}

func (r *readByteStream) closeStream() error {
	var err error
	r.closeOnce.Do(func() {
		err = r.stream.Close()
	})
	return err
}
