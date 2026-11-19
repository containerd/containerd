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
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/containerd/console"
	google_protobuf "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"github.com/containerd/errdefs"
	runc "github.com/containerd/go-runc"
)

// failTimeout bounds waits for things that must happen, so only a failure
// ever waits that long. settle is how long something that must NOT happen yet
// is observed; a correct implementation cannot fail because of it.
const (
	failTimeout = 30 * time.Second
	settle      = 50 * time.Millisecond
)

// gate blocks whoever calls Close or block until released, and tells the test
// when it has been entered. It is joined with the goroutines started through
// it when the test ends, after being released, so none outlives the test.
type gate struct {
	entered chan struct{}
	unblock chan struct{}
	enter   func()
	release func()
	wg      sync.WaitGroup
}

func newGate(t *testing.T) *gate {
	t.Helper()
	g := &gate{entered: make(chan struct{}), unblock: make(chan struct{})}
	g.enter = sync.OnceFunc(func() { close(g.entered) })
	g.release = sync.OnceFunc(func() { close(g.unblock) })
	t.Cleanup(func() {
		g.release()
		g.wg.Wait()
	})
	return g
}

// Close is called by Delete in its cleanup, when g is in a closers list.
func (g *gate) Close() error {
	g.enter()
	<-g.unblock
	return nil
}

// run calls fn in a goroutine and returns a channel closed when it returns.
func (g *gate) run(fn func()) <-chan struct{} {
	done := make(chan struct{})
	g.wg.Go(func() {
		defer close(done)
		fn()
	})
	return done
}

func finishes(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func mustFinish(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	if !finishes(done, failTimeout) {
		t.Fatalf("timed out waiting for %s", what)
	}
}

// fakeRuntime returns an OCI runtime on which every command succeeds ("true")
// or fails ("false").
func fakeRuntime(t *testing.T, command string) *runc.Runc {
	t.Helper()
	path, err := exec.LookPath(command)
	if err != nil {
		t.Skipf("%s not found: %v", command, err)
	}
	return &runc.Runc{Command: path}
}

func newInit(t *testing.T, command string) *Init {
	t.Helper()
	p := New("test", fakeRuntime(t, command), stdio.Stdio{})
	p.Platform = recordingPlatform{events: new([]string)}
	p.io = &processIO{}
	return p
}

func newExec(t *testing.T) *execProcess {
	t.Helper()
	e := &execProcess{id: "exec", path: t.TempDir(), parent: newInit(t, "true"), waitBlock: make(chan struct{}), io: &processIO{}}
	e.execState = &execCreatedState{p: e}
	return e
}

// blockedDelete starts p.Delete and returns once it is blocked in g's Close,
// i.e. in its cleanup. finish lets it proceed and returns its result.
func blockedDelete(t *testing.T, g *gate, p Process) (finish func() error) {
	t.Helper()
	var err error
	done := g.run(func() { err = p.Delete(t.Context()) })
	select {
	case <-g.entered:
	case <-done:
		t.Fatalf("Delete returned without reaching its cleanup: %v", err)
	case <-time.After(failTimeout):
		t.Fatal("timed out waiting for Delete to reach its cleanup")
	}
	return func() error {
		g.release()
		mustFinish(t, done, "Delete")
		return err
	}
}

// state is what State reports for a process.
type state struct {
	status string
	exit   int
	at     time.Time
}

func (s state) equal(o state) bool {
	return s.status == o.status && s.exit == o.exit && s.at.Equal(o.at)
}

func observe(t *testing.T, p Process) state {
	t.Helper()
	s, err := p.Status(t.Context())
	if err != nil {
		t.Errorf("Status: %v", err)
	}
	return state{s, p.ExitStatus(), p.ExitedAt()}
}

// testStateDuringDelete checks that, while the Delete of p is in its cleanup,
// State returns and reports what it did before the Delete; that, if the Delete
// fails, nothing has changed after it; and that after a successful one the
// process is stopped.
func testStateDuringDelete(t *testing.T, g *gate, p Process, deleteFails bool) {
	t.Helper()
	want := observe(t, p)
	finish := blockedDelete(t, g, p)
	var during state
	mustFinish(t, g.run(func() { during = observe(t, p) }), "State during Delete")
	if !during.equal(want) {
		t.Errorf("State during Delete = %+v; want %+v", during, want)
	}
	err := finish()
	if (err != nil) != deleteFails {
		t.Errorf("Delete: %v; want error: %v", err, deleteFails)
	}
	after := observe(t, p)
	if deleteFails && !after.equal(want) {
		t.Errorf("State after failed Delete = %+v; want %+v", after, want)
	}
	if !deleteFails && !after.equal(state{"stopped", want.exit, want.at}) {
		t.Errorf("State after Delete = %+v; want stopped with %d, %v", after, want.exit, want.at)
	}
}

func TestStateDuringDeleteOfCreatedInit(t *testing.T) {
	for _, tc := range []struct {
		name, runtime string
		fails         bool
	}{{"DeleteFails", "false", true}, {"DeleteSucceeds", "true", false}} {
		t.Run(tc.name, func(t *testing.T) {
			g, p := newGate(t), newInit(t, tc.runtime)
			p.closers = []io.Closer{g}
			if s := observe(t, p); !s.equal(state{"created", 0, time.Time{}}) {
				t.Fatalf("State = %+v; want created", s)
			}
			testStateDuringDelete(t, g, p, tc.fails)
		})
	}
}

func TestStateDuringDeleteOfCreatedExec(t *testing.T) {
	g, e := newGate(t), newExec(t)
	e.closers = []io.Closer{g}
	testStateDuringDelete(t, g, e, false)
}

// A stopped process keeps its real exit status and time during the cleanup.
func TestStateDuringDeleteOfStoppedInit(t *testing.T) {
	g, p := newGate(t), newInit(t, "true")
	p.closers = []io.Closer{g}
	p.SetExited(137)
	if s := observe(t, p); s.status != "stopped" || s.exit != 137 || s.at.IsZero() {
		t.Fatalf("State = %+v; want stopped with exit 137 and a time", s)
	}
	testStateDuringDelete(t, g, p, false)
}

func TestStateDuringDeleteOfStoppedExec(t *testing.T) {
	g, e := newGate(t), newExec(t)
	e.closers = []io.Closer{g}
	e.SetExited(3)
	if s := observe(t, e); s.status != "stopped" || s.exit != 3 || s.at.IsZero() {
		t.Fatalf("State = %+v; want stopped with exit 3 and a time", s)
	}
	testStateDuringDelete(t, g, e, false)
}

// An exit that arrives during a Delete that then fails waits for the cleanup
// and is applied to the process afterwards.
func TestExitDuringFailedDelete(t *testing.T) {
	g, p := newGate(t), newInit(t, "false")
	p.closers = []io.Closer{g}
	finish := blockedDelete(t, g, p)
	exited := g.run(func() { p.SetExited(9) })
	if finishes(exited, settle) {
		t.Error("SetExited did not wait for Delete")
	}
	if err := finish(); err == nil {
		t.Error("Delete succeeded with a failing runtime")
	}
	mustFinish(t, exited, "SetExited")
	mustFinish(t, g.run(func() { p.Wait(t.Context()) }), "Wait")
	if s := observe(t, p); s.status != "stopped" || s.exit != 9 {
		t.Errorf("State = %+v; want stopped with exit 9", s)
	}
}

// startStub is the state of an Init whose Start blocks in g and then succeeds.
type startStub struct {
	initState
	g *gate
}

func (s startStub) Start(context.Context) error {
	s.g.enter()
	<-s.g.unblock
	return nil
}

func (startStub) Status(context.Context) (string, error) { return "running", nil }

// Status waits for an in-flight Start and so reports "running", not
// "created"; CRI recovery kills tasks it sees as created.
func TestStatusWaitsForStart(t *testing.T) {
	g, p := newGate(t), newInit(t, "true")
	p.initState = startStub{p.initState, g}
	var startErr error
	started := g.run(func() { startErr = p.Start(t.Context()) })
	select {
	case <-g.entered:
	case <-started:
		t.Fatalf("Start returned before blocking: %v", startErr)
	case <-time.After(failTimeout):
		t.Fatal("timed out waiting for Start")
	}
	var status string
	got := g.run(func() { status, _ = p.Status(t.Context()) })
	if finishes(got, settle) {
		t.Errorf("Status returned %q during Start", status)
	}
	g.release()
	mustFinish(t, started, "Start")
	mustFinish(t, got, "Status")
	if status != "running" || startErr != nil {
		t.Errorf("Status = %q, Start: %v; want \"running\", nil", status, startErr)
	}
}

type operation struct {
	name     string
	call     func() error
	notFound bool // the deleted process reports it gone
}

// While the cleanup runs, every method that changes a process waits for it,
// then runs against the deleted process.
func testOperationsWait(t *testing.T, g *gate, p Process, ops []operation) {
	t.Helper()
	finish := blockedDelete(t, g, p)
	errs := make([]error, len(ops))
	done := make([]<-chan struct{}, len(ops))
	for i, op := range ops {
		done[i] = g.run(func() { errs[i] = op.call() })
	}
	time.Sleep(settle)
	for i, op := range ops {
		select {
		case <-done[i]:
			t.Errorf("%s did not wait for Delete", op.name)
		default:
		}
	}
	if err := finish(); err != nil {
		t.Errorf("Delete: %v", err)
	}
	for i, op := range ops {
		mustFinish(t, done[i], op.name)
		if op.notFound && !errdefs.IsNotFound(errs[i]) {
			t.Errorf("%s: %v; want not found", op.name, errs[i])
		}
	}
}

func TestInitOperationsWaitForDelete(t *testing.T) {
	g, p := newGate(t), newInit(t, "true")
	p.closers = []io.Closer{g}
	ctx := t.Context()
	testOperationsWait(t, g, p, []operation{
		{"Start", func() error { return p.Start(ctx) }, false},
		{"SetExited", func() error { p.SetExited(1); return nil }, false},
		{"Delete", func() error { return p.Delete(ctx) }, true},
		{"Resize", func() error { return p.Resize(console.WinSize{}) }, false},
		{"Pause", func() error { return p.Pause(ctx) }, false},
		{"Resume", func() error { return p.Resume(ctx) }, false},
		{"Kill", func() error { return p.Kill(ctx, 9, false) }, true},
		{"KillAll", func() error { return p.KillAll(ctx) }, false},
		{"Exec", func() error { _, err := p.Exec(ctx, "", &ExecConfig{}); return err }, false},
		{"Checkpoint", func() error { return p.Checkpoint(ctx, &CheckpointConfig{}) }, false},
		{"Update", func() error { return p.Update(ctx, &google_protobuf.Any{}) }, false},
	})
}

func TestExecOperationsWaitForDelete(t *testing.T) {
	g, e := newGate(t), newExec(t)
	e.closers = []io.Closer{g}
	ctx := t.Context()
	testOperationsWait(t, g, e, []operation{
		{"Start", func() error { return e.Start(ctx) }, false},
		{"SetExited", func() error { e.SetExited(1); return nil }, false},
		{"Delete", func() error { return e.Delete(ctx) }, true},
		{"Resize", func() error { return e.Resize(console.WinSize{}) }, false},
		{"Kill", func() error { return e.Kill(ctx, 9, false) }, true},
	})
}

// Every reader takes mu, so it waits for a method that holds it, such as an
// in-flight Start. Only the exec's own mu is held for the exec readers.
func TestReadersTakeMu(t *testing.T) {
	p, e := newInit(t, "true"), newExec(t)
	p.SetExited(7)
	e.SetExited(3)
	for _, tc := range []struct {
		name string
		mu   *sync.Mutex
		read func() any
		want any
	}{
		{"Init.Status", &p.mu, func() any { s, _ := p.Status(t.Context()); return s }, "stopped"},
		{"Init.ExitStatus", &p.mu, func() any { return p.ExitStatus() }, 7},
		{"Init.ExitedAt", &p.mu, func() any { return !p.ExitedAt().IsZero() }, true},
		{"Exec.Status", &e.mu, func() any { s, _ := e.Status(t.Context()); return s }, "stopped"},
		{"Exec.ExitStatus", &e.mu, func() any { return e.ExitStatus() }, 3},
		{"Exec.ExitedAt", &e.mu, func() any { return !e.ExitedAt().IsZero() }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGate(t)
			var got any
			tc.mu.Lock()
			done := g.run(func() { got = tc.read() })
			returned := finishes(done, settle)
			tc.mu.Unlock()
			if returned {
				t.Error("returned while mu was held")
			}
			mustFinish(t, done, tc.name)
			if got != tc.want {
				t.Errorf("got %v; want %v", got, tc.want)
			}
		})
	}
}
