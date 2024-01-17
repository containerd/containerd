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

package content

import (
	"bytes"
	"context"
	_ "crypto/sha256" // required by go-digest
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/pkg/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
)

type copySource struct {
	reader io.Reader
	size   int64
	digest digest.Digest
}

func TestCopy(t *testing.T) {
	defaultSource := newCopySource("this is the source to copy")

	cf1 := func(buf *bytes.Buffer, st Status) commitFunction {
		i := 0
		return func() error {
			// function resets the first time
			if i == 0 {
				// this is the case where, the pipewriter to which the data was being written has
				// changed. which means we need to clear the buffer
				i++
				buf.Reset()
				st.Offset = 0
				return ErrReset
			}
			return nil
		}
	}

	cf2 := func(buf *bytes.Buffer, st Status) commitFunction {
		i := 0
		return func() error {
			// function resets for more than the maxReset value
			if i < maxResets+1 {
				// this is the case where, the pipewriter to which the data was being written has
				// changed. which means we need to clear the buffer
				i++
				buf.Reset()
				st.Offset = 0
				return ErrReset
			}
			return nil
		}
	}

	s1 := Status{}
	s2 := Status{}
	b1 := bytes.Buffer{}
	b2 := bytes.Buffer{}

	var testcases = []struct {
		name        string
		source      copySource
		writer      fakeWriter
		expected    string
		expectedErr error
	}{
		{
			name:   "copy no offset",
			source: defaultSource,
			writer: fakeWriter{
				Buffer: &bytes.Buffer{},
			},
			expected: "this is the source to copy",
		},
		{
			name:   "copy with offset from seeker",
			source: defaultSource,
			writer: fakeWriter{
				Buffer: &bytes.Buffer{},
				status: Status{Offset: 8},
			},
			expected: "the source to copy",
		},
		{
			name:   "copy with offset from unseekable source",
			source: copySource{reader: bytes.NewBufferString("foobar"), size: 6},
			writer: fakeWriter{
				Buffer: &bytes.Buffer{},
				status: Status{Offset: 3},
			},
			expected: "bar",
		},
		{
			name:   "commit already exists",
			source: newCopySource("this already exists"),
			writer: fakeWriter{
				Buffer: &bytes.Buffer{},
				commitFunc: func() error {
					return errdefs.ErrAlreadyExists
				}},
			expected: "this already exists",
		},
		{
			name:   "commit fails first time with ErrReset",
			source: newCopySource("content to copy"),
			writer: fakeWriter{
				Buffer:     &b1,
				status:     s1,
				commitFunc: cf1(&b1, s1),
			},
			expected: "content to copy",
		},
		{
			name:   "write fails more than maxReset times due to reset",
			source: newCopySource("content to copy"),
			writer: fakeWriter{
				Buffer:     &b2,
				status:     s2,
				commitFunc: cf2(&b2, s2),
			},
			expected:    "",
			expectedErr: fmt.Errorf("failed to copy after %d retries", maxResets),
		},
	}

	for _, testcase := range testcases {
		testcase := testcase
		t.Run(testcase.name, func(t *testing.T) {
			err := Copy(context.Background(),
				&testcase.writer,
				testcase.source.reader,
				testcase.source.size,
				testcase.source.digest)

			// if an error is expected then further comparisons are not required
			if testcase.expectedErr != nil {
				assert.Equal(t, testcase.expectedErr, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, testcase.source.digest, testcase.writer.committedDigest)
			assert.Equal(t, testcase.expected, testcase.writer.String())
		})
	}
}

func newCopySource(raw string) copySource {
	return copySource{
		reader: strings.NewReader(raw),
		size:   int64(len(raw)),
		digest: digest.FromBytes([]byte(raw)),
	}
}

type commitFunction func() error

type fakeWriter struct {
	*bytes.Buffer
	committedDigest digest.Digest
	status          Status
	commitFunc      commitFunction
}

func (f *fakeWriter) Close() error {
	f.Buffer.Reset()
	return nil
}

func (f *fakeWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...Opt) error {
	f.committedDigest = expected
	if f.commitFunc == nil {
		return nil
	}
	return f.commitFunc()
}

func (f *fakeWriter) Digest() digest.Digest {
	return f.committedDigest
}

func (f *fakeWriter) Status() (Status, error) {
	return f.status, nil
}

func (f *fakeWriter) Truncate(size int64) error {
	f.Buffer.Truncate(int(size))
	return nil
}
