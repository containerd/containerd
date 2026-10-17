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
	"testing"

	"github.com/containerd/console"

	"github.com/containerd/containerd/v2/pkg/stdio"
)

type closeCounter struct{ closed int }

func (c *closeCounter) Close() error {
	c.closed++
	return nil
}

type closeCountingConsole struct {
	console.Console
	closed int
}

func (c *closeCountingConsole) Close() error {
	c.closed++
	return nil
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
	if stdin.closed != 1 {
		t.Fatalf("stdin FIFO writer closed %d times, want 1", stdin.closed)
	}
	if cons.closed != 1 {
		t.Fatalf("console closed %d times, want 1", cons.closed)
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
