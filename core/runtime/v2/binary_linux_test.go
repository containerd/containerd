//go:build linux

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

package v2

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCopyShimlogStopsOnDone(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer writer.Close()

	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("failed to create temp stderr: %v", err)
	}
	defer stderrFile.Close()

	oldStderr := os.Stderr
	os.Stderr = stderrFile
	defer func() { os.Stderr = oldStderr }()

	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- copyShimlog(context.Background(), "", reader, done)
	}()

	time.Sleep(20 * time.Millisecond)
	close(done)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("copyShimlog returned unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("copyShimlog did not exit after done was closed")
	}
}

func TestCopyShimlogFallbackWithoutFD(t *testing.T) {
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("failed to create temp stderr: %v", err)
	}
	defer stderrFile.Close()

	oldStderr := os.Stderr
	os.Stderr = stderrFile
	defer func() { os.Stderr = oldStderr }()

	done := make(chan struct{})
	err = copyShimlog(context.Background(), "", io.NopCloser(strings.NewReader("hello")), done)
	if err != nil {
		t.Fatalf("copyShimlog fallback returned unexpected error: %v", err)
	}
}
