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
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connResetError returns a connection-reset error shaped the way it
// surfaces from a TCP read: a *net.OpError wrapping an *os.SyscallError
// wrapping the raw errno.
func connResetError() error {
	return &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: os.NewSyscallError("read", syscall.ECONNRESET),
	}
}

// flakyReader serves from data, failing with failErr after serving limit
// bytes. A natural io.EOF is returned once data is exhausted.
type flakyReader struct {
	data    []byte
	limit   int
	served  int
	failErr error
}

func (r *flakyReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	if r.served >= r.limit {
		return 0, r.failErr
	}
	n := min(len(p), r.limit-r.served, len(r.data))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	r.served += n
	return n, nil
}

func (r *flakyReader) Close() error { return nil }

// retryableReadErrors are the error classes for which Read retries by
// reopening the body at the current offset.
var retryableReadErrors = []struct {
	name string
	err  error
}{
	{"unexpected EOF", io.ErrUnexpectedEOF},
	{"connection reset", connResetError()},
}

func TestHTTPReadSeekerReopensOnRetryableError(t *testing.T) {
	for _, tc := range retryableReadErrors {
		t.Run(tc.name, func(t *testing.T) {
			content := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
			const serveChunk = 16

			var openOffsets []int64
			open := func(offset int64) (io.ReadCloser, error) {
				openOffsets = append(openOffsets, offset)
				return &flakyReader{
					data:    content[offset:],
					limit:   serveChunk,
					failErr: tc.err,
				}, nil
			}

			rs, err := newHTTPReadSeeker(int64(len(content)), open)
			require.NoError(t, err)
			defer rs.Close()

			got, err := io.ReadAll(rs)
			require.NoError(t, err, "transfer should complete across mid-body errors when progress is made")
			assert.Equal(t, content, got, "reassembled content should match despite reconnects")
			assert.Equal(t, []int64{0, 16, 32, 48}, openOffsets,
				"each reopen should be at the advanced offset, preserving progress")
		})
	}
}

func TestHTTPReadSeekerAbortsAfterRetriesWithNoProgress(t *testing.T) {
	for _, tc := range retryableReadErrors {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			open := func(offset int64) (io.ReadCloser, error) {
				opens++
				assert.EqualValues(t, 0, offset, "no progress was made, so every reopen should be at offset 0")
				// limit of zero: every read fails without progress
				return &flakyReader{data: []byte("0123"), limit: 0, failErr: tc.err}, nil
			}

			rs, err := newHTTPReadSeeker(4, open)
			require.NoError(t, err)
			defer rs.Close()

			_, err = io.ReadAll(rs)
			require.Error(t, err, "retries with no progress should eventually surface the error")
			require.ErrorIs(t, err, tc.err)
			assert.Equal(t, 1+maxRetry, opens, "one initial open plus maxRetry reopens")
		})
	}
}

func TestIsRetryableReadError(t *testing.T) {
	assert.True(t, isRetryableReadError(io.ErrUnexpectedEOF))
	assert.True(t, isRetryableReadError(fmt.Errorf("read: %w", io.ErrUnexpectedEOF)))
	assert.True(t, isRetryableReadError(connResetError()))
	assert.True(t, isRetryableReadError(syscall.ECONNRESET))
	assert.False(t, isRetryableReadError(io.EOF))
	assert.False(t, isRetryableReadError(errors.New("some other error")))
	assert.False(t, isRetryableReadError(syscall.ECONNREFUSED))
}
