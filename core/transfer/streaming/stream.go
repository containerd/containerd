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
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
)

const maxRead = 32 * 1024
const windowSize = 2 * maxRead

var bufPool = &sync.Pool{
	New: func() any {
		buffer := make([]byte, maxRead)
		return &buffer
	},
}

func SendStream(ctx context.Context, r io.Reader, stream streaming.Stream) {
	ctx, cancel := context.WithCancelCause(ctx)
	var remaining atomic.Int32
	windowUpdated := make(chan struct{}, 1)
	var closeOnce sync.Once
	var closeReaderOnce sync.Once
	closeStream := func() {
		closeOnce.Do(func() {
			_ = stream.Close()
		})
	}
	closeReader := func() {
		rc, ok := r.(io.Closer)
		if !ok {
			return
		}
		closeReaderOnce.Do(func() {
			_ = rc.Close()
		})
	}
	context.AfterFunc(ctx, func() {
		closeReader()
		closeStream()
	})
	go func() {
		defer cancel(nil)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			anyType, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
					cancel(err)
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
				remaining.Add(v.Update)
				select {
				case <-ctx.Done():
					return
				case windowUpdated <- struct{}{}:
				default:
				}
			default:
				log.G(ctx).Errorf("unexpected stream object of type %T", i)
			}
		}
	}()
	go func() {
		defer closeStream()

		buf := bufPool.Get().(*[]byte)
		defer bufPool.Put(buf)

		for {
			rem := remaining.Load()
			if rem > 0 {
				// Don't wait for window update since there are remaining
				select {
				case <-ctx.Done():
					// TODO: Send error message on stream before close to allow remote side to return error
					return
				case <-windowUpdated:
				default:
				}
			} else {
				// Wait for window update
				select {
				case <-ctx.Done():
					// TODO: Send error message on stream before close to allow remote side to return error
					return
				case <-windowUpdated:
					rem = remaining.Load()
				}
			}

			if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
				return
			}

			var max int32 = maxRead
			if max > rem {
				max = rem
			}
			b := (*buf)[:max]
			n, readErr := r.Read(b)
			if n > 0 {
				remaining.Add(-int32(n))

				data := &transferapi.Data{
					Data: b[:n],
				}
				anyType, err := typeurl.MarshalAny(data)
				if err != nil {
					log.G(ctx).WithError(err).Errorf("failed to marshal data for send")
					// TODO: Send error message on stream before close to allow remote side to return error
					return
				}
				if err := stream.Send(anyType); err != nil {
					if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
						return
					}
					log.G(ctx).WithError(err).Errorf("send failed")
					return
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					log.G(ctx).WithError(readErr).Errorf("read failed")
				}
				return
			}
		}
	}()
}

func ReceiveStream(ctx context.Context, stream streaming.Stream) io.Reader {
	r, w := io.Pipe()
	go func() {
		defer stream.Close()
		var window int32
		for {
			var werr error
			if window < windowSize {
				update := &transferapi.WindowUpdate{
					Update: windowSize,
				}
				anyType, err := typeurl.MarshalAny(update)
				if err != nil {
					w.CloseWithError(fmt.Errorf("failed to marshal window update: %w", err))
					return
				}
				// check window update error after recv, stream may be complete
				if werr = stream.Send(anyType); werr == nil {
					window += windowSize
				} else if errors.Is(werr, io.EOF) {
					// TODO: Why does send return EOF here
					werr = nil
				}
			}
			anyType, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					err = nil
				} else {
					err = fmt.Errorf("received failed: %w", err)
				}
				w.CloseWithError(err)
				return
			} else if werr != nil {
				// Try receive before erroring out
				w.CloseWithError(fmt.Errorf("failed to send window update: %w", werr))
				return
			}
			i, err := typeurl.UnmarshalAny(anyType)
			if err != nil {
				w.CloseWithError(fmt.Errorf("failed to unmarshal received object: %w", err))
				return
			}
			switch v := i.(type) {
			case *transferapi.Data:
				n, err := w.Write(v.Data)
				if err != nil {
					w.CloseWithError(fmt.Errorf("failed to unmarshal received object: %w", err))
					// Close will error out sender
					return
				}
				window = window - int32(n)
			// TODO: Handle error case
			default:
				log.G(ctx).Warnf("Ignoring unknown stream object of type %T", i)
				continue
			}
		}

	}()

	return r
}

func GenerateID(prefix string) string {
	t := time.Now()
	var b [3]byte
	rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", prefix, t.Nanosecond(), base64.URLEncoding.EncodeToString(b[:]))
}
