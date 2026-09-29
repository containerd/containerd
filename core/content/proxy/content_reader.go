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

package proxy

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	"github.com/containerd/errdefs/pkg/errgrpc"
	digest "github.com/opencontainers/go-digest"
)

// errReaderAtClosed is the error of reading a released stream, such as after
// the ReaderAt is closed.
var errReaderAtClosed = errors.New("content: reader closed")

type remoteReaderAt struct {
	ctx    context.Context
	digest digest.Digest
	size   int64
	client contentapi.TTRPCContentClient

	// streams holds the open streams of readers returned from Reader, so
	// Close can release them. newRemoteReaderAt initializes it; Close then
	// sets it to nil, so nil means closed and open releases a new stream
	// straight away.
	mu      sync.Mutex
	streams map[*stream]struct{}
}

// newRemoteReaderAt returns a remoteReaderAt ready to open streams.
func newRemoteReaderAt(ctx context.Context, dgst digest.Digest, size int64, client contentapi.TTRPCContentClient) *remoteReaderAt {
	return &remoteReaderAt{
		ctx:     ctx,
		digest:  dgst,
		size:    size,
		client:  client,
		streams: map[*stream]struct{}{},
	}
}

func (ra *remoteReaderAt) Size() int64 {
	return ra.size
}

func (ra *remoteReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	rr := &contentapi.ReadContentRequest{
		Digest: ra.digest.String(),
		Offset: off,
		Size:   int64(len(p)),
	}
	// we need a child context with cancel, or the eventually called
	// grpc.NewStream will leak the goroutine until the whole thing is cleared.
	// See comment at https://godoc.org/google.golang.org/grpc#ClientConn.NewStream
	childCtx, cancel := context.WithCancel(ra.ctx)
	// we MUST cancel the child context; see comment above
	defer cancel()
	rc, err := ra.client.Read(childCtx, rr)
	if err != nil {
		return 0, errgrpc.ToNative(err)
	}

	for len(p) > 0 {
		var resp *contentapi.ReadContentResponse
		// fill our buffer up until we can fill p.
		resp, err = rc.Recv()
		if err != nil {
			if err == io.EOF {
				return n, err
			}
			return n, errgrpc.ToNative(err)
		}

		copied := copy(p, resp.Data)
		n += copied
		p = p[copied:]
	}
	return n, nil
}

// Reader returns a reader for the whole content which receives it over Read
// streams. Sequential consumers, such as decompression, would otherwise open a
// new stream for every ReadAt call.
//
// The content is requested in windows of readWindow bytes, one stream per
// window, so that no transport has to hold the whole content. The next window
// is requested while the current one is consumed, keeping a single window read
// ahead. Streams are released when the content is fully read, on error, or when
// the ReaderAt is closed.
func (ra *remoteReaderAt) Reader() io.Reader {
	return &remoteReader{ra: ra}
}

// Close releases the open streams of readers returned from Reader.
func (ra *remoteReaderAt) Close() error {
	ra.mu.Lock()
	streams := ra.streams
	ra.streams = nil
	ra.mu.Unlock()
	for s := range streams {
		s.release()
	}
	return nil
}

// readWindow is the content requested by a single Read stream. Neither gRPC nor
// ttrpc bounds the content a reader holds in memory to the window a stream is
// read in: a gRPC stream grows its flow control window to the bandwidth-delay
// product, and a ttrpc stream buffers a fixed number of messages regardless of
// their size. Requesting the content in windows, one read ahead, bounds a
// reader to about two windows independent of the transport.
//
// It is a variable so tests can use a smaller window.
var readWindow int64 = 2 << 20

// remoteReader reads content sequentially over Read streams of readWindow
// bytes, keeping one window read ahead of the caller.
type remoteReader struct {
	ra *remoteReaderAt

	// cur is the stream being read, next is the window read ahead, nil until
	// the first read or once the end of the content is reached.
	cur  *stream
	next *stream

	// offset is the position of the caller in the content.
	offset int64
	err    error
}

func (r *remoteReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if r.cur == nil {
			if r.offset >= r.ra.size {
				return 0, r.fail(io.EOF)
			}
			// Request the next window while this one is consumed.
			r.cur = r.ra.open(r.offset)
			r.readAhead()
		}

		n, err := r.cur.read(p)
		r.offset += int64(n)
		if err == io.EOF {
			// The window is fully read. Advance to the read-ahead window,
			// if any, and continue, requesting the one after it.
			r.cur.release()
			r.cur = r.next
			r.next = nil
			if r.cur != nil {
				r.readAhead()
			}
			if n > 0 {
				return n, nil
			}
			continue
		}
		if err != nil {
			return n, r.fail(err)
		}
		return n, nil
	}
}

// readAhead opens the window after the current one, if any remains.
func (r *remoteReader) readAhead() {
	end := r.cur.end
	if end >= r.ra.size {
		return
	}
	r.next = r.ra.open(end)
}

// fail records err as the terminal state of the reader and releases its
// streams.
func (r *remoteReader) fail(err error) error {
	r.err = err
	r.cur.release()
	r.cur = nil
	r.next.release()
	r.next = nil
	return err
}

// releaseTimeout bounds how long a released stream is received to its end
// before its RPC is cancelled. A ttrpc client only forgets a stream once it
// receives the stream's final message, so a released stream is received to
// its end rather than cancelled; the timeout only applies to a server which
// stops sending.
//
// It is a variable so tests can use a shorter timeout.
var releaseTimeout = 10 * time.Second

// open starts a stream for the window of readWindow bytes, or less at the end
// of the content, beginning at offset. The stream is opened and received in
// the background, so a read never waits for another stream to make progress.
func (ra *remoteReaderAt) open(offset int64) *stream {
	size := min(readWindow, ra.size-offset)
	ctx, cancel := context.WithCancelCause(ra.ctx)
	s := &stream{
		ra:     ra,
		offset: offset,
		end:    offset + size,
		ctx:    ctx,
		cancel: cancel,
		ready:  make(chan struct{}, 1),
	}

	ra.mu.Lock()
	closed := ra.streams == nil
	if !closed {
		ra.streams[s] = struct{}{}
	}
	ra.mu.Unlock()
	if closed {
		s.release()
		return s
	}

	go s.receive(size, releaseTimeout)
	return s
}

// stream receives one window of content over a single Read stream.
type stream struct {
	ra     *remoteReaderAt
	buf    []byte
	offset int64
	// end is the offset at which the stream's request ends.
	end int64

	// ctx is done once the stream is released or the caller's context is
	// done; receive then discards the rest of the stream.
	ctx    context.Context
	cancel context.CancelCauseFunc

	// mu guards the content received and not yet read, at most the size
	// requested, and the end of the stream. receive signals ready when it
	// adds to either.
	mu       sync.Mutex
	received [][]byte
	ended    bool
	err      error
	ready    chan struct{}
}

// receive opens the stream and receives it to its end, keeping its content
// for read until ctx is done. The content is received whether or not it is
// read, so a stream never stalls its transport: a gRPC stream frees its slot
// for the next, and a ttrpc stream's message queue does not fill.
func (s *stream) receive(size int64, timeout time.Duration) {
	rctx, rcancel := context.WithCancel(context.WithoutCancel(s.ra.ctx))
	defer rcancel()
	stop := context.AfterFunc(s.ctx, func() {
		time.AfterFunc(timeout, rcancel)
	})
	defer stop()

	err := func() error {
		rc, err := s.ra.client.Read(rctx, &contentapi.ReadContentRequest{
			Digest: s.ra.digest.String(),
			Offset: s.offset,
			Size:   size,
		})
		if err != nil {
			return err
		}
		for {
			resp, err := rc.Recv()
			if err != nil {
				return err
			}
			data := resp.Data
			s.mu.Lock()
			if s.ctx.Err() == nil && size > 0 {
				data = data[:min(int64(len(data)), size)]
				size -= int64(len(data))
				s.received = append(s.received, data)
			}
			s.mu.Unlock()
			s.signal()
		}
	}()

	s.mu.Lock()
	s.ended = true
	if err != io.EOF {
		s.err = errgrpc.ToNative(err)
	}
	s.mu.Unlock()
	s.signal()
}

func (s *stream) signal() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

// read copies the next received content into p. It returns io.EOF once the
// window is fully read.
func (s *stream) read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		if s.offset >= s.end {
			return 0, io.EOF
		}
		if s.ctx.Err() != nil {
			return 0, context.Cause(s.ctx)
		}
		s.mu.Lock()
		if len(s.received) > 0 {
			s.buf = s.received[0]
			s.received[0] = nil
			s.received = s.received[1:]
			s.mu.Unlock()
			continue
		}
		ended, err := s.ended, s.err
		s.mu.Unlock()
		if ended {
			if err != nil {
				return 0, err
			}
			// The stream ended before the end of its request.
			return 0, io.ErrUnexpectedEOF
		}
		select {
		case <-s.ready:
		case <-s.ctx.Done():
			return 0, context.Cause(s.ctx)
		}
	}

	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	s.offset += int64(n)
	return n, nil
}

// release stops keeping content for read, leaving receive to discard the rest
// of the stream, and removes the stream from the ReaderAt. It is safe to call
// on a nil stream, concurrently and more than once.
func (s *stream) release() {
	if s == nil {
		return
	}
	s.cancel(errReaderAtClosed)
	s.mu.Lock()
	s.received = nil
	s.mu.Unlock()
	s.ra.mu.Lock()
	delete(s.ra.streams, s)
	s.ra.mu.Unlock()
}
