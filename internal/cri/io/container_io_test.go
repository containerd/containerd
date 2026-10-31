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
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	cioutil "github.com/containerd/containerd/v2/pkg/ioutil"
)

// writerFunc adapts a function to an io.WriteCloser.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
func (writerFunc) Close() error                  { return nil }

// attempts is how often the scenario is repeated. Whether the end of stdin
// is handled before the registrations in a given attempt is up to the
// scheduler: code that ends the session by removing writers that may not be
// added yet hangs in at least one attempt in four.
const attempts = 50

// A client whose stdin is already at EOF attaches, with the container's stdin
// left open, while the container is busy writing output. WriterGroup holds
// its lock while it writes, so the session's writers cannot be registered
// until that write finishes. Stdin is forwarded in the meantime and ends
// before the writers are registered. The session has to end all the same.
func TestAttachEndsWhenStdinEndsBeforeOutputIsRegistered(t *testing.T) {
	for range attempts {
		stdinWritten, outputWritten := make(chan struct{}), make(chan struct{})
		var once sync.Once
		c := &ContainerIO{
			id:          "test",
			stdoutGroup: cioutil.NewWriterGroup(),
			stderrGroup: cioutil.NewWriterGroup(),
			stdioStream: &stdioStream{stdin: writerFunc(func(p []byte) (int, error) {
				// Stdin ends right after the container's write below is over,
				// when Attach has yet to be woken up to register its writers.
				once.Do(func() { close(stdinWritten); <-outputWritten })
				return len(p), nil
			})},
		}

		// Keep stdout busy: it is registered first, so both registrations
		// are still to come when stdin ends.
		writing, release := make(chan struct{}), make(chan struct{})
		c.stdoutGroup.Add("log", writerFunc(func(p []byte) (int, error) {
			close(writing)
			<-release
			return len(p), nil
		}))
		go func() {
			c.stdoutGroup.Write([]byte("container output"))
			close(outputWritten)
		}()
		<-writing

		done := make(chan struct{})
		go func() {
			defer close(done)
			c.Attach(t.Context(), AttachOptions{
				Stdin:  strings.NewReader("abcd1234"),
				Stdout: cioutil.NewNopWriteCloser(io.Discard),
				Stderr: cioutil.NewNopWriteCloser(io.Discard),
			})
		}()

		select {
		case <-stdinWritten:
		case <-time.After(10 * time.Second):
			t.Fatal("stdin was not forwarded while the writers were waiting to be registered")
		}
		close(release)

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Attach did not return after stdin reached EOF")
		}
	}
}
