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
	"io"
	"strings"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/gotestyourself/gotestyourself/assert"
	is "github.com/gotestyourself/gotestyourself/assert/cmp"
	"github.com/opencontainers/go-digest"
)

type copySource struct {
	reader io.Reader
	size   int64
	digest digest.Digest
}

func TestCopy(t *testing.T) {
	defaultSource := newCopySource("this is the source to copy")

	var testcases = []struct {
		name     string
		source   copySource
		writer   fakeWriter
		expected string
	}{
		{
			name:     "copy no offset",
			source:   defaultSource,
			writer:   fakeWriter{},
			expected: "this is the source to copy",
		},
		{
			name:     "copy with offset from seeker",
			source:   defaultSource,
			writer:   fakeWriter{status: Status{Offset: 8}},
			expected: "the source to copy",
		},
		{
			name:     "copy with offset from unseekable source",
			source:   copySource{reader: bytes.NewBufferString("foobar"), size: 6},
			writer:   fakeWriter{status: Status{Offset: 3}},
			expected: "bar",
		},
		{
			name:   "commit already exists",
			source: defaultSource,
			writer: fakeWriter{commitFunc: func() error {
				return errdefs.ErrAlreadyExists
			}},
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			err := Copy(context.Background(),
				&testcase.writer,
				testcase.source.reader,
				testcase.source.size,
				testcase.source.digest)

			assert.NilError(t, err)
			assert.Check(t, is.Equal(testcase.source.digest, testcase.writer.commitedDigest))
			assert.Check(t, is.Equal(testcase.expected, testcase.writer.String()))
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

type fakeWriter struct {
	bytes.Buffer
	commitedDigest digest.Digest
	status         Status
	commitFunc     func() error
}

func (f *fakeWriter) Close() error {
	f.Buffer.Reset()
	return nil
}

func (f *fakeWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...Opt) error {
	f.commitedDigest = expected
	if f.commitFunc == nil {
		return nil
	}
	return f.commitFunc()
}

func (f *fakeWriter) Digest() digest.Digest {
	return f.commitedDigest
}

func (f *fakeWriter) Status() (Status, error) {
	return f.status, nil
}

func (f *fakeWriter) Truncate(size int64) error {
	f.Buffer.Truncate(int(size))
	return nil
}
