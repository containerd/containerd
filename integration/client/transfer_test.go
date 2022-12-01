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

package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/pkg/transfer/archive"
)

func TestTransferEcho(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()
	t.Run("ImportExportEchoBig", newImportExportEcho(ctx, client, bytes.Repeat([]byte("somecontent"), 17*1024)))
	t.Run("ImportExportEchoSmall", newImportExportEcho(ctx, client, []byte("somecontent")))
	t.Run("ImportExportEchoEmpty", newImportExportEcho(ctx, client, []byte("")))

}

func newImportExportEcho(ctx context.Context, client *containerd.Client, expected []byte) func(*testing.T) {
	return func(t *testing.T) {
		testBuf := newWaitBuffer()
		err := client.Transfer(ctx, archive.NewImageImportStream(bytes.NewReader(expected), "application/octet-stream"), archive.NewImageExportStream(testBuf, "application/octet-stream"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(expected, testBuf.Bytes()) {
			t.Fatalf("Bytes did not match\n\tActual:   %v\n\tExpected: %v", displayBytes(testBuf.Bytes()), displayBytes(expected))
		}
	}
}

type WriteBytesCloser interface {
	io.WriteCloser
	Bytes() []byte
}

func newWaitBuffer() WriteBytesCloser {
	return &waitBuffer{
		Buffer: bytes.NewBuffer(nil),
		closed: make(chan struct{}),
	}
}

type waitBuffer struct {
	*bytes.Buffer
	closed chan struct{}
}

func (wb *waitBuffer) Close() error {
	select {
	case <-wb.closed:
	default:
		close(wb.closed)
	}
	return nil
}

func (wb *waitBuffer) Bytes() []byte {
	<-wb.closed
	return wb.Buffer.Bytes()
}

func displayBytes(b []byte) string {
	if len(b) > 40 {
		skipped := len(b) - 20
		return fmt.Sprintf("%s...(skipped %d)...%s", b[:10], skipped, b[len(b)-10:])
	}
	return string(b)
}
