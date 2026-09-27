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
	"bytes"
	"io"
	"testing"
)

// TestReadByteStream races under -race unless the window counter is atomic.
func TestReadByteStream(t *testing.T) {
	expected := bytes.Repeat([]byte("containerd"), windowSize/4)

	rs, ws := pipeStream()
	w := WriteByteStream(t.Context(), ws)
	r := ReadByteStream(t.Context(), rs)

	go func() {
		io.Copy(w, bytes.NewReader(expected))
		w.Close()
	}()

	actual, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatalf("read %d bytes, want %d", len(actual), len(expected))
	}
}
