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
	"github.com/stretchr/testify/require"
)

func TestWrapReadCloser(t *testing.T) {
	buf := bytes.NewBufferString("abc")

	rc := NewWrapReadCloser(buf)
	dst := make([]byte, 1)
	n, err := rc.Read(dst)
	assert.Equal(t, 1, n)
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), dst)

	n, err = rc.Read(dst)
	assert.Equal(t, 1, n)
	require.NoError(t, err)
	assert.Equal(t, []byte("b"), dst)

	rc.Close()
	n, err = rc.Read(dst)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, []byte("b"), dst)
}
