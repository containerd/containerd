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
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConReader(t *testing.T) {
	src := bytes.NewBufferString("abc")

	con := newConReader(src, 4, 0)

	dst := make([]byte, 1)

	n, err := con.Read(dst)
	assert.Equal(t, 1, n)
	assert.NoError(t, err)
	assert.Equal(t, []byte("a"), dst)

	n, err = con.Read(dst)
	assert.Equal(t, 1, n)
	assert.NoError(t, err)
	assert.Equal(t, []byte("b"), dst)

	con.Close()

	n, err = con.Read(dst)
	assert.Equal(t, 1, n)
	assert.Equal(t, nil, err)
	assert.Equal(t, []byte("c"), dst)

	n, err = con.Read(dst)
	assert.Equal(t, 0, n)
	assert.Equal(t, ErrClosed, err)
	assert.Equal(t, []byte("c"), dst)
}

func TestConReaderShortBuffer(t *testing.T) {
	src := bytes.NewBufferString("aaaaaaaaaabbbbbbbbbbcccccccccc")

	con := newConReader(src, 4, 10)

	dst := make([]byte, 10)
	n, err := con.Read(dst)
	assert.Equal(t, 10, n)
	assert.NoError(t, err)
	assert.Equal(t, []byte("aaaaaaaaaa"), dst)

	dst = make([]byte, 15)
	n, err = con.Read(dst)
	assert.Equal(t, 15, n)
	assert.NoError(t, err)
	assert.Equal(t, []byte("bbbbbbbbbbccccc"), dst)

	n, err = con.Read(dst)
	assert.Equal(t, 5, n)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, []byte("ccccc"), dst[:n])

	n, err = con.Read(dst)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, []byte("ccccc"), dst[:5])
}

func TestConReaderClose(t *testing.T) {
	src := bytes.NewBufferString("aaaaaaaaaabbbbbbbbbbcccccccccc")

	// If channel size is not zero, When conReader is closed, the return error
	// is Uncertain. It depends on the select to choose quit chan or buf chan.
	con := newConReader(src, 4, 0)

	dst := make([]byte, 10)
	n, err := con.Read(dst)
	assert.Equal(t, 10, n)
	assert.NoError(t, err)
	assert.Equal(t, []byte("aaaaaaaaaa"), dst)

	con.Close()

	dst = make([]byte, 15)
	n, err = con.Read(dst)
	assert.Equal(t, 2, n)
	assert.Equal(t, ErrClosed, err)
	assert.Equal(t, []byte("bb"), dst[:n])

	n, err = con.Read(dst)
	assert.Equal(t, 0, n)
	assert.Equal(t, ErrClosed, err)
	assert.Equal(t, []byte("bb"), dst[:2])
}
