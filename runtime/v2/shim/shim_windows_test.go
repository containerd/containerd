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
		t.Fatalf("failed to read len %v bytes read: %v", len(expected), actual)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatalf("expected '%v' != actual '%v'", expected, actual)
	}
}

func writeValueTo(wr io.Writer, value string, t *testing.T) {
	expected := []byte(value)
	written, err := wr.Write(expected)
	if err != nil {
		t.Fatalf("failed to write with: %v", err)
	}
	if len(expected) != written {
		t.Fatalf("failed to write len %v bytes wrote: %v", len(expected), written)
	}
}

func runOneTest(ns, id string, writer io.Writer, t *testing.T) {
	// Write on closed
	go writeValueTo(writer, "Hello World!", t)

	// Connect
	c, err := winio.DialPipe(fmt.Sprintf("\\\\.\\pipe\\containerd-shim-%s-%s-log", ns, id), nil)
	if err != nil {
		t.Fatal("should have successfully connected to log")
	}
	defer c.Close()

	// Read the deferred buffer.
	readValueFrom(c, "Hello World!", t)

	go writeValueTo(writer, "Hello Next!", t)
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
