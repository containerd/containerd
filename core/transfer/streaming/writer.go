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
	wbs := &writeByteStream{
		ctx:     ctx,
		stream:  stream,
		updated: make(chan struct{}, 1),
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			anyType, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
					log.G(ctx).WithError(err).Error("send byte stream ended without EOF")
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
				// Window credit is a positive additive delta; zero or negative is malformed and would
				// stall the stream or slice the outgoing [:max] out of range.
				if v.Update <= 0 {
					log.G(ctx).Errorf("ignoring non-positive window update: %d", v.Update)
					continue
				}
				wbs.addWindow(v.Update)
				select {
				case <-ctx.Done():
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
	stream    streaming.Stream
	remaining atomic.Int64
	updated   chan struct{}
}

// addWindow adds a positive credit to remaining. Accumulating in int64 keeps every
// advertised credit; no realistic number of int32 updates can overflow it back to a
// negative slice bound.
func (wbs *writeByteStream) addWindow(update int32) {
	wbs.remaining.Add(int64(update))
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
			case <-wbs.updated:
				continue
			}
		}
		var max int32 = maxRead
		// Compare widths in int so a len(p) above MaxInt32 does not narrow to a
		// negative int32 and reintroduce a negative slice bound.
		if int(max) > len(p) {
			max = int32(len(p))
		}
		// remaining is int64; only this branch runs when it is below max, and then it
		// is smaller than maxRead so it fits back into int32.
		if int64(max) > remaining {
			max = int32(remaining)
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
			log.G(wbs.ctx).WithError(err).Errorf("send failed")
			return
		}
		n += int(max)
		p = p[max:]
		wbs.remaining.Add(-int64(max))
	}
	return
}

func (wbs *writeByteStream) Close() error {
	return wbs.stream.Close()
}
