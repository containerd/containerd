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

package ioutil

import (
	"errors"
	"io"
	"sync"
)

const defaultBufSize = 32 * 1024

var ErrClosed = errors.New("closed reader")

var bufPool = &sync.Pool{
	New: func() interface{} { return newReaderBuf(defaultBufSize) },
}

type readerBuf struct {
	buf  []byte
	r, n int
	err  error
}

func newReaderBuf(customBufSize int) *readerBuf {
	return &readerBuf{buf: make([]byte, customBufSize), n: 0, err: nil}
}

func (rb *readerBuf) Buffered() int {
	return rb.n - rb.r
}

func (rb *readerBuf) Read(p []byte) (n int, err error) {
	pn := len(p)
	buffered := rb.Buffered()
	if pn < buffered {
		n = copy(p, rb.buf[rb.r:rb.r+pn])
		rb.r += n
		return n, nil
	}
	// Large read, empty buffer
	n = copy(p[:buffered], rb.buf[rb.r:])
	rb.r += n
	return n, rb.err
}

func (rb *readerBuf) ReadFrom(r io.Reader) (n int64, err error) {
	rb.n, rb.err = r.Read(rb.buf)
	return int64(rb.n), rb.err
}

func (rb *readerBuf) Reset() {
	rb.r = 0
	rb.n = 0
	rb.err = nil
}

// The conReader provides a concurrent Reader.
// It will start a goroutine in the background to read data into the// buffer, then send buffer to chan, The foreground conReader will
// receive the buffer from chan and return it to the caller.
// NOTE: The conReader itself is not concurrency safe.
// NOTE: The conReader must be eventually closed by the caller,
// to avoid goroutine leakage.
type conReader struct {
	buf     *readerBuf
	ch      chan *readerBuf
	wg      *sync.WaitGroup
	quit    chan struct{}
	err     error
	bufPool bool
}

func NewConReader(r io.Reader) io.ReadCloser {
	return newConReader(r, 0, 8)
}

func newConReader(r io.Reader, customBufSize int, chanSize int) io.ReadCloser {
	ch := make(chan *readerBuf, chanSize)
	wg := &sync.WaitGroup{}
	quit := make(chan struct{})
	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			var buf *readerBuf
			if customBufSize > 0 {
				buf = newReaderBuf(customBufSize)
			} else {
				buf = bufPool.Get().(*readerBuf)
			}
			_, err := buf.ReadFrom(r)

			select {
			case <-quit:
				return
			case ch <- buf:
			}

			if err != nil {
				return
			}
		}
	}()
	return &conReader{buf: nil, ch: ch, wg: wg, quit: quit, err: nil, bufPool: !(customBufSize > 0)}
}

func (cr *conReader) Read(p []byte) (n int, err error) {
	if cr.err != nil {
		return 0, cr.err
	}
	need := len(p)

	for n < need {
		if cr.buf == nil {
			select {
			case buf := <-cr.ch:
				cr.buf = buf
			case <-cr.quit:
				return n, ErrClosed
			}
		}

		br, err := cr.buf.Read(p[n:])
		n += br
		if cr.buf.Buffered() == 0 {
			cr.err = err
			if cr.bufPool {
				cr.buf.Reset()
				bufPool.Put(cr.buf)
			}
			cr.buf = nil
		}
		if err != nil {
			return n, err
		}
	}
	return n, err
}

func (cr *conReader) Close() error {
	close(cr.quit)
	cr.wg.Wait()
	return nil
}
