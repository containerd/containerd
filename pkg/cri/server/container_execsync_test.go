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

package server

import (
	"bytes"
	"testing"

	cioutil "github.com/containerd/containerd/pkg/ioutil"
	"github.com/stretchr/testify/assert"
)

func TestCWWrite(t *testing.T) {
	var buf bytes.Buffer
	cw := &cappedWriter{w: cioutil.NewNopWriteCloser(&buf), remain: 10}

	n, err := cw.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)

	n, err = cw.Write([]byte("helloworld"))
	assert.Equal(t, []byte("hellohello"), buf.Bytes(), "partial write")
	assert.Equal(t, 5, n)
	assert.ErrorIs(t, err, errNoRemain)

	_, err = cw.Write([]byte("world"))
	assert.ErrorIs(t, err, errNoRemain)
}

func TestCWClose(t *testing.T) {
	var buf bytes.Buffer
	cw := &cappedWriter{w: cioutil.NewNopWriteCloser(&buf), remain: 5}
	err := cw.Close()
	assert.NoError(t, err)
}
