// +build windows

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

package shim

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/namespaces"
)

func readValueFrom(rdr io.Reader, expectedStr string, t *testing.T) {
	expected := []byte(expectedStr)
	actual := make([]byte, len(expected))
	read, err := rdr.Read(actual)
	if err != nil {
		t.Fatalf("failed to read with: %v", err)
	}
	if read != len(expected) {
		t.Fatalf("failed to read len %v bytes read: %v", len(expected), read)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatalf("expected '%v' != actual '%v'", expected, actual)
	}
}

func writeValueTo(wr io.Writer, value string) {
	expected := []byte(value)
	written, err := wr.Write(expected)
	if err != nil {
		panic(fmt.Sprintf("failed to write with: %v", err))
	}
	if len(expected) != written {
		panic(fmt.Sprintf("failed to write len %v bytes wrote: %v", len(expected), written))
	}
}

func runOneTest(ns, id string, writer io.Writer, t *testing.T) {
	// Write on closed
	go writeValueTo(writer, "Hello World!")

	// Connect
	c, err := winio.DialPipe(fmt.Sprintf("\\\\.\\pipe\\containerd-shim-%s-%s-log", ns, id), nil)
	if err != nil {
		t.Fatal("should have successfully connected to log")
	}
	defer c.Close()

	// Read the deferred buffer.
	readValueFrom(c, "Hello World!", t)

	go writeValueTo(writer, "Hello Next!")
	readValueFrom(c, "Hello Next!", t)
}

func TestOpenLog(t *testing.T) {
	ns := "openlognamespace"
	id := "openlogid"
	ctx := namespaces.WithNamespace(context.TODO(), ns)
	writer, err := openLog(ctx, id)
	if err != nil {
		t.Fatalf("failed openLog with %v", err)
	}

	// Do three iterations of write/open/read/write/read/close
	for i := 0; i < 3; i++ {
		runOneTest(ns, id, writer, t)
	}
}

func TestBlockingBufferWriteNotEnoughCapacity(t *testing.T) {
	bb := newBlockingBuffer(5)
	val := make([]byte, 10)
	w, err := bb.Write(val)
	if err == nil {
		t.Fatal("write should of failed capacity check")
	}
	if w != 0 {
		t.Fatal("write should of not written any bytes on failed capacity check")
	}
}

func TestBlockingBufferLoop(t *testing.T) {
	nameBytes := []byte(t.Name())
	nameBytesLen := len(nameBytes)
	bb := newBlockingBuffer(nameBytesLen)
	for i := 0; i < 3; i++ {
		writeValueTo(bb, t.Name())
		if bb.Len() != nameBytesLen {
			t.Fatalf("invalid buffer bytes len after write: (%d)", bb.buffer.Len())
		}
		buf := &bytes.Buffer{}
		w, err := bb.WriteTo(buf)
		if err != nil {
			t.Fatalf("should not have failed WriteTo: (%v)", err)
		}
		if w != int64(nameBytesLen) {
			t.Fatalf("should have written all bytes, wrote (%d)", w)
		}
		readValueFrom(buf, t.Name(), t)
		if bb.Len() != 0 {
			t.Fatalf("invalid buffer bytes len after read: (%d)", bb.buffer.Len())
		}
	}
}
func TestBlockingBuffer(t *testing.T) {
	nameBytes := []byte(t.Name())
	nameBytesLen := len(nameBytes)
	bb := newBlockingBuffer(nameBytesLen)

	// Write the first value
	writeValueTo(bb, t.Name())
	if bb.Len() != nameBytesLen {
		t.Fatalf("buffer len != %d", nameBytesLen)
	}

	// We should now have filled capacity the next write should block
	done := make(chan struct{})
	go func() {
		writeValueTo(bb, t.Name())
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("third write should of blocked")
	case <-time.After(10 * time.Millisecond):
		buff := &bytes.Buffer{}
		_, err := bb.WriteTo(buff)
		if err != nil {
			t.Fatalf("failed to drain buffer with: %v", err)
		}
		if bb.Len() != 0 {
			t.Fatalf("buffer len != %d", 0)
		}
		readValueFrom(buff, t.Name(), t)
	}
	<-done
	if bb.Len() != nameBytesLen {
		t.Fatalf("buffer len != %d", nameBytesLen)
	}
}
