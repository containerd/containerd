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

package local

import (
	"bufio"
	"bytes"
	"context"
	_ "crypto/sha256"
	"fmt"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"

	"github.com/containerd/containerd/v2/core/content"
)

func FuzzContentStoreWriter(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		cs, err := NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		cw, err := cs.Writer(ctx, content.WithRef("myref"))
		if err != nil {
			return
		}
		if err := cw.Close(); err != nil {
			return
		}

		// reopen, so we can test things
		cw, err = cs.Writer(ctx, content.WithRef("myref"))
		if err != nil {
			return
		}

		err = checkCopyFuzz(int64(len(data)), cw, bufio.NewReader(io.NopCloser(bytes.NewReader(data))))
		if err != nil {
			t.Fatal(err)
		}
		expected := digest.FromBytes(data)

		if err = cw.Commit(ctx, int64(len(data)), expected); err != nil {
			return
		}
	})
}

func checkCopyFuzz(size int64, dst io.Writer, src io.Reader) error {
	nn, err := io.Copy(dst, src)
	if err != nil {
		return err
	}

	if nn != size {
		return fmt.Errorf("short write, expected: %d, got %d", size, nn)
	}
	return nil
}
