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

package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
)

const maxRetry = 3

type httpReadSeeker struct {
	size               int64
	offset             int64
	rc                 io.ReadCloser
	open               func(offset int64) (io.ReadCloser, error)
	closed             bool
	errsWithNoProgress int

	activity ActivityTrackerInterface

	deadline time.Time
	clock    Clock

	mu sync.Mutex
}

func newHTTPReadSeeker(size int64, open func(offset int64) (io.ReadCloser, error), activity ...ActivityTrackerInterface) (io.ReadCloser, error) {
	return newHTTPReadSeekerWithClock(size, open, realClock{}, activity...)
}

func newHTTPReadSeekerWithClock(size int64, open func(offset int64) (io.ReadCloser, error), clock Clock, activity ...ActivityTrackerInterface) (io.ReadCloser, error) {
	hrs := &httpReadSeeker{
		size:  size,
		open:  open,
		clock: clock,
	}
	if len(activity) > 0 {
		hrs.activity = activity[0]
	}
	return hrs, nil
}

func newHTTPReadSeekerWithClockAndDeadline(size int64, open func(offset int64) (io.ReadCloser, error), clock Clock, deadline time.Time, activity ...ActivityTrackerInterface) (io.ReadCloser, error) {
	hrs := &httpReadSeeker{
		size:     size,
		open:     open,
		clock:    clock,
		deadline: deadline,
	}
	if len(activity) > 0 {
		hrs.activity = activity[0]
	}
	return hrs, nil
}

func (hrs *httpReadSeeker) Read(p []byte) (int, error) {
	for {
		hrs.mu.Lock()
		if hrs.closed {
			hrs.mu.Unlock()
			return 0, io.EOF
		}

		if !hrs.deadline.IsZero() {
			if hrs.clock.Now().After(hrs.deadline) {
				hrs.mu.Unlock()
				return 0, context.DeadlineExceeded
			}
		}
		hrs.mu.Unlock()

		rd, readerErr := hrs.reader()
		if readerErr != nil {
			return 0, readerErr
		}

		hrs.mu.Lock()
		if !hrs.deadline.IsZero() {
			if hrs.clock.Now().After(hrs.deadline) {
				hrs.mu.Unlock()
				return 0, context.DeadlineExceeded
			}
		}
		expectedRc := hrs.rc
		hrs.mu.Unlock()

		var n int
		var readErr error
		n, readErr = rd.Read(p)

		hrs.mu.Lock()

		if expectedRc != hrs.rc {
			hrs.mu.Unlock()
			continue
		}

		if !hrs.deadline.IsZero() && hrs.clock.Now().After(hrs.deadline) {
			if n == 0 {
				hrs.mu.Unlock()
				return 0, context.DeadlineExceeded
			}
		}
		hrs.offset += int64(n)
		if n > 0 {
			if hrs.activity != nil {
				hrs.activity.Touch()
			}
		}
		if n > 0 || readErr == nil {
			hrs.errsWithNoProgress = 0
		}
		switch readErr {
		case io.ErrUnexpectedEOF:
			if n == 0 {
				hrs.errsWithNoProgress++
				if hrs.errsWithNoProgress > maxRetry {
					hrs.mu.Unlock()
					return n, readErr
				}
			}
			if hrs.rc != nil {
				if clsErr := hrs.rc.Close(); clsErr != nil {
					log.L.WithError(clsErr).Error("httpReadSeeker: failed to close ReadCloser")
				}
				hrs.rc = nil
			}
			if _, err2 := hrs.readerLocked(); err2 == nil {
				hrs.mu.Unlock()
				return n, nil
			}
		case io.EOF:
			if hrs.rc != nil {
				if clsErr := hrs.rc.Close(); clsErr != nil {
					log.L.WithError(clsErr).Error("httpReadSeeker: failed to close ReadCloser after io.EOF")
				}
				hrs.rc = nil
			}
		}
		hrs.mu.Unlock()
		return n, readErr
	}
}

func (hrs *httpReadSeeker) Close() error {
	hrs.mu.Lock()
	defer hrs.mu.Unlock()
	if hrs.closed {
		return nil
	}
	hrs.closed = true
	if hrs.rc != nil {
		return hrs.rc.Close()
	}

	return nil
}

func (hrs *httpReadSeeker) Seek(offset int64, whence int) (int64, error) {
	hrs.mu.Lock()
	defer hrs.mu.Unlock()
	if hrs.closed {
		return 0, fmt.Errorf("httpReadSeeker.Seek: closed: %w", errdefs.ErrUnavailable)
	}

	abs := hrs.offset
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs += offset
	case io.SeekEnd:
		if hrs.size == -1 {
			return 0, fmt.Errorf("httpReadSeeker.Seek: unknown size, cannot seek from end: %w", errdefs.ErrUnavailable)
		}
		abs = hrs.size + offset
	default:
		return 0, fmt.Errorf("httpReadSeeker.Seek: invalid whence: %w", errdefs.ErrInvalidArgument)
	}

	if abs < 0 {
		return 0, fmt.Errorf("httpReadSeeker.Seek: negative offset: %w", errdefs.ErrInvalidArgument)
	}

	if abs != hrs.offset {
		if hrs.rc != nil {
			if err := hrs.rc.Close(); err != nil {
				log.L.WithError(err).Error("httpReadSeeker.Seek: failed to close ReadCloser")
			}

			hrs.rc = nil
		}

		hrs.offset = abs
	}

	return hrs.offset, nil
}

func (hrs *httpReadSeeker) reader() (io.Reader, error) {
	hrs.mu.Lock()
	defer hrs.mu.Unlock()
	return hrs.readerLocked()
}

func (hrs *httpReadSeeker) readerLocked() (io.Reader, error) {
	if hrs.rc != nil {
		return hrs.rc, nil
	}

	if hrs.size == -1 || hrs.offset < hrs.size {
		if hrs.open == nil {
			return nil, fmt.Errorf("cannot open: %w", errdefs.ErrNotImplemented)
		}

		rc, err := hrs.open(hrs.offset)
		if err != nil {
			return nil, fmt.Errorf("httpReadSeeker: failed open: %w", err)
		}

		if hrs.rc != nil {
			if err := hrs.rc.Close(); err != nil {
				log.L.WithError(err).Error("httpReadSeeker: failed to close ReadCloser")
			}
		}
		hrs.rc = rc
	} else {
		hrs.rc = io.NopCloser(bytes.NewReader([]byte{}))
	}

	return hrs.rc, nil
}
