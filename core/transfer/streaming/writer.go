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
	"sync"
	"sync/atomic"

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
)

func WriteByteStream(ctx context.Context, stream streaming.Stream) io.WriteCloser {
	ctx, cancel := context.WithCancelCause(ctx)
	wbs := &writeByteStream{
		ctx:     ctx,
		cancel:  cancel,
		stream:  stream,
		updated: make(chan struct{}, 1),
	}
	context.AfterFunc(wbs.ctx, func() { _ = wbs.closeStream() })
	go func() {
		defer cancel(nil)
		for {
			select {
			case <-wbs.ctx.Done():
				return
			default:
			}
			anyType, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
					wbs.cancel(err)
				}
				return
			}
			i, err := typeurl.UnmarshalAny(anyType)
			if err != nil {
				log.G(ctx).WithError(err).Error("failed to unmarshal stream object")
				continue
			}
			switch v := i.(type) {
			case *transferapi.WindowUpdate:
				wbs.remaining.Add(v.Update)
				select {
				case <-wbs.ctx.Done():
					return
				case wbs.updated <- struct{}{}:
				default:
					// Don't block if no writes are waiting
				}
			default:
				log.G(ctx).Errorf("unexpected stream object of type %T", i)
			}
		}
	}()

	return wbs
}

type writeByteStream struct {
	ctx       context.Context
	cancel    context.CancelCauseFunc
	stream    streaming.Stream
	closeOnce sync.Once
	remaining atomic.Int32
	updated   chan struct{}
}

func (wbs *writeByteStream) Write(p []byte) (n int, err error) {
	for len(p) > 0 {
		if cause := context.Cause(wbs.ctx); cause != nil && !errors.Is(cause, context.Canceled) {
			return n, cause
		}

		remaining := wbs.remaining.Load()
		if remaining == 0 {
			// Wait for window update
			select {
			case <-wbs.ctx.Done():
				if cause := context.Cause(wbs.ctx); cause != nil && !errors.Is(cause, context.Canceled) {
					return n, cause
				}
				// TODO: Send error message on stream before close to allow remote side to return error
				err = io.ErrShortWrite
				return
			case <-wbs.updated:
				continue
			}
		}
		var max int32 = maxRead
		if max > int32(len(p)) {
			max = int32(len(p))
		}
		if max > remaining {
			max = remaining
		}

		data := &transferapi.Data{
			Data: p[:max],
		}
		var anyType typeurl.Any
		anyType, err = typeurl.MarshalAny(data)
		if err != nil {
			log.G(wbs.ctx).WithError(err).Errorf("failed to marshal data for send")
			// TODO: Send error message on stream before close to allow remote side to return error
			return
		}
		if err = wbs.stream.Send(anyType); err != nil {
			if cause := context.Cause(wbs.ctx); cause != nil && !errors.Is(cause, context.Canceled) {
				return n, cause
			}
			log.G(wbs.ctx).WithError(err).Errorf("send failed")
			return
		}
		n += int(max)
		p = p[max:]
		wbs.remaining.Add(-max)
	}
	return
}

func (wbs *writeByteStream) Close() error {
	if wbs.cancel != nil {
		wbs.cancel(nil)
	}
	return wbs.closeStream()
}

func (wbs *writeByteStream) closeStream() error {
	var err error
	wbs.closeOnce.Do(func() {
		err = wbs.stream.Close()
	})
	return err
}
