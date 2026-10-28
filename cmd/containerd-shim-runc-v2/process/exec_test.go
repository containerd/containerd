//go:build !windows

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

package process

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/console"
	runc "github.com/containerd/go-runc"

	"github.com/containerd/containerd/v2/pkg/stdio"
)

type closeCounter struct{ closed atomic.Int32 }

func (c *closeCounter) Close() error {
	c.closed.Add(1)
	return nil
}

type closeCountingConsole struct {
	console.Console
	closed atomic.Int32
}

func (c *closeCountingConsole) Close() error {
	c.closed.Add(1)
	return nil
}

// waitReleased waits for the stdin writer and the console to be closed once each.
func waitReleased(t *testing.T, stdin *closeCounter, cons *closeCountingConsole) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for stdin.closed.Load() != 1 || cons.closed.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("stdin FIFO writer closed %d times and console %d times, want 1 and 1", stdin.closed.Load(), cons.closed.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// exitingExec is a running terminal exec whose I/O handles count their closes.
func exitingExec(t *testing.T, stdin *closeCounter, cons *closeCountingConsole) *execProcess {
	t.Helper()
	var events []string
	e := &execProcess{
		id:        "tty",
		path:      t.TempDir(),
		parent:    &Init{Platform: recordingPlatform{events: &events}},
		stdin:     stdin,
		closers:   []io.Closer{stdin},
		console:   cons,
		waitBlock: make(chan struct{}),
	}
	e.execState = &execRunningState{p: e}
	return e
}

// An exec that exits releases its I/O once the copiers drain, with no Delete:
// CRI cannot send one for an exec it lost across a containerd restart. A later
// delete finds nothing left to close.
func TestExecExitReleasesTerminalIO(t *testing.T) {
	stdin := &closeCounter{}
	cons := &closeCountingConsole{}
	e := exitingExec(t, stdin, cons)
	e.SetExited(0)
	waitReleased(t, stdin, cons)
	if err := e.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := stdin.closed.Load(); n != 1 {
		t.Fatalf("stdin FIFO writer closed %d times after delete, want 1", n)
	}
	if n := cons.closed.Load(); n != 1 {
		t.Fatalf("console closed %d times after delete, want 1", n)
	}
}

// The release after exit waits for the copiers, so output still in flight
// reaches the client before the handles close.
func TestExecExitReleaseWaitsForCopiers(t *testing.T) {
	stdin := &closeCounter{}
	cons := &closeCountingConsole{}
	e := exitingExec(t, stdin, cons)
	e.wg.Add(1)
	e.SetExited(0)
	time.Sleep(50 * time.Millisecond)
	if n := stdin.closed.Load(); n != 0 {
		t.Fatalf("stdin FIFO writer closed %d times before the copiers drained", n)
	}
	e.wg.Done()
	waitReleased(t, stdin, cons)
}

// A terminal exec has no processIO; the shim's writer on the stdin FIFO and
// the console master are the handles it holds, and delete must release both
// so the console's stdin copier can end.
func TestExecDeleteReleasesTerminalIO(t *testing.T) {
	stdin := &closeCounter{}
	cons := &closeCountingConsole{}
	var events []string
	e := &execProcess{
		id:      "tty",
		path:    t.TempDir(),
		parent:  &Init{Platform: recordingPlatform{events: &events}},
		stdin:   stdin,
		closers: []io.Closer{stdin},
		console: cons,
	}
	if err := e.delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := stdin.closed.Load(); n != 1 {
		t.Fatalf("stdin FIFO writer closed %d times, want 1", n)
	}
	if n := cons.closed.Load(); n != 1 {
		t.Fatalf("console closed %d times, want 1", n)
	}
}

// recordingPlatform notes the order of console operations.
type recordingPlatform struct {
	stdio.Platform
	events *[]string
}

func (p recordingPlatform) ShutdownConsole(context.Context, console.Console) error {
	*p.events = append(*p.events, "shutdown")
	return nil
}

type orderedConsole struct {
	console.Console
	events *[]string
}

func (c orderedConsole) Close() error {
	*c.events = append(*c.events, "close")
	return nil
}

// A process deleted from the created state never ran setExited, so delete
// must shut the console down itself, and before it closes the master.
func TestExecDeleteShutsConsoleDownBeforeClosing(t *testing.T) {
	var events []string
	e := &execProcess{
		id:      "created-tty",
		path:    t.TempDir(),
		parent:  &Init{Platform: recordingPlatform{events: &events}},
		console: orderedConsole{events: &events},
	}
	if err := e.delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(events, ","); got != "shutdown,close" {
		t.Fatalf("console operations = %q, want shutdown,close", got)
	}
}

type closeCountingIO struct {
	runc.IO
	closed atomic.Int32
}

func (c *closeCountingIO) Close() error {
	c.closed.Add(1)
	return nil
}

// A logging binary reads the process pipes until delete closes them; it has
// no copiers for the exit path to wait on, so the exit path must leave it
// alone or the logger is terminated while a descendant may still write.
func TestExecExitLeavesLoggingBinaryToDelete(t *testing.T) {
	stdin := &closeCounter{}
	pio := &closeCountingIO{}
	var events []string
	e := &execProcess{
		id:        "binary",
		path:      t.TempDir(),
		parent:    &Init{Platform: recordingPlatform{events: &events}},
		stdin:     stdin,
		closers:   []io.Closer{stdin},
		io:        &processIO{io: pio, copy: false},
		waitBlock: make(chan struct{}),
	}
	e.execState = &execRunningState{p: e}
	e.SetExited(0)
	time.Sleep(100 * time.Millisecond)
	if n := pio.closed.Load(); n != 0 {
		t.Fatalf("logging binary I/O closed %d times at exit, want 0 until delete", n)
	}
	if err := e.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := pio.closed.Load(); n != 1 {
		t.Fatalf("logging binary I/O closed %d times after delete, want 1", n)
	}
	if n := stdin.closed.Load(); n != 1 {
		t.Fatalf("stdin FIFO writer closed %d times after delete, want 1", n)
	}
}
