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
	"sync/atomic"

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
)

func WriteByteStream(ctx context.Context, stream streaming.Stream) io.WriteCloser {
	ctx, cancel := context.WithCancel(ctx)
	wbs := &writeByteStream{
		ctx:     ctx,
		cancel:  cancel,
		stream:  stream,
		updated: make(chan struct{}, 1),
		errCh:   make(chan error, 1),
	}
	go func() {
		defer cancel()
		for {
			select {
			case <-wbs.ctx.Done():
				return
			default:
			}

			anyType, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
					select {
					case wbs.errCh <- err:
					case <-wbs.ctx.Done():
					default:
					}
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
	cancel    context.CancelFunc
	stream    streaming.Stream
	remaining atomic.Int32
	updated   chan struct{}
	errCh     chan error
}

func (wbs *writeByteStream) Write(p []byte) (n int, err error) {
	for len(p) > 0 {
		remaining := wbs.remaining.Load()
		if remaining == 0 {
			// Don't wait for window update since there are remaining
			select {
			case <-wbs.ctx.Done():
				// TODO: Send error message on stream before close to allow remote side to return error
				err = io.ErrShortWrite
				return
			case err = <-wbs.errCh:
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
		// TODO: continue
		// remaining = remaining - int32(n)

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
			log.G(wbs.ctx).WithError(err).Errorf("send failed")
			return
		}
		n += int(max)
		p = p[max:]
		wbs.remaining.Add(-1 * max)
	}
	return
}

func (wbs *writeByteStream) Close() error {
	if wbs.cancel != nil {
		wbs.cancel()
	}
	return wbs.stream.Close()
}
