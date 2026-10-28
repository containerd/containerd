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

package io

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/containerd/containerd/v2/pkg/cio"
	cioutil "github.com/containerd/containerd/v2/pkg/ioutil"
)

func TestContainerIOAttachReceivesOutputTriggeredByStdin(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(oldProcs)

	// Repeat the operation because the original bug depends on the scheduler
	// running the stdin copy goroutine before the output writers are added.
	for i := range 1000 {
		if err := runContainerIOAttachTest(); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

func runContainerIOAttachTest() error {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()

	containerIO := &ContainerIO{
		id:    "test",
		fifos: &cio.FIFOSet{},
		stdioStream: &stdioStream{
			stdin:  stdinWriter,
			stdout: stdoutReader,
			stderr: stderrReader,
		},
		stdoutGroup: cioutil.NewWriterGroup(),
		stderrGroup: cioutil.NewWriterGroup(),
		closer: &wgCloser{
			wg: &sync.WaitGroup{},
		},
	}

	var logStdout, logStderr bytes.Buffer
	containerIO.stdoutGroup.Add("log-stdout", cioutil.NewNopWriteCloser(&logStdout))
	containerIO.stderrGroup.Add("log-stderr", cioutil.NewNopWriteCloser(&logStderr))
	containerIO.Pipe()

	containerDone := make(chan struct{})
	go func() {
		defer close(containerDone)
		data, _ := io.ReadAll(stdinReader)
		_, _ = stdoutWriter.Write(data)
		_, _ = stderrWriter.Write(data)
		_, _ = stdoutWriter.Write([]byte("stdin closed\n"))
		_, _ = stderrWriter.Write([]byte("stdin closed\n"))
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
	}()

	var attachStdout, attachStderr bytes.Buffer
	containerIO.Attach(context.Background(), AttachOptions{
		Stdin:     strings.NewReader("attached\n"),
		Stdout:    cioutil.NewNopWriteCloser(&attachStdout),
		Stderr:    cioutil.NewNopWriteCloser(&attachStderr),
		StdinOnce: true,
		CloseStdin: func() error {
			return nil
		},
	})
	containerIO.Wait()
	<-containerDone

	const expected = "attached\nstdin closed\n"
	if got := logStdout.String(); got != expected {
		return fmt.Errorf("container stdout log = %q, want %q", got, expected)
	}
	if got := logStderr.String(); got != expected {
		return fmt.Errorf("container stderr log = %q, want %q", got, expected)
	}
	if got := attachStdout.String(); got != expected {
		return fmt.Errorf("attach stdout = %q, want %q", got, expected)
	}
	if got := attachStderr.String(); got != expected {
		return fmt.Errorf("attach stderr = %q, want %q", got, expected)
	}
	return nil
}
