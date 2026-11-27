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

package task

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/containerd/console"
	taskAPI "github.com/containerd/containerd/api/runtime/task/v3"
	runcC "github.com/containerd/go-runc"

	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/runc"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/pkg/stdio"
)

// runningExec is an exec that has already been started. Its Start matches
// execRunningState: the call fails and the pid stays set.
type runningExec struct {
	id  string
	pid int
}

func (p *runningExec) ID() string                               { return p.id }
func (p *runningExec) Pid() int                                 { return p.pid }
func (p *runningExec) ExitStatus() int                          { return 0 }
func (p *runningExec) ExitedAt() time.Time                      { return time.Time{} }
func (p *runningExec) Stdin() io.Closer                         { return nil }
func (p *runningExec) Stdio() stdio.Stdio                       { return stdio.Stdio{} }
func (p *runningExec) Status(context.Context) (string, error)   { return "running", nil }
func (p *runningExec) Wait(context.Context) error               { return nil }
func (p *runningExec) Resize(console.WinSize) error             { return nil }
func (p *runningExec) Start(context.Context) error              { return errAlreadyStarted }
func (p *runningExec) Delete(context.Context) error             { return nil }
func (p *runningExec) Kill(context.Context, uint32, bool) error { return nil }
func (p *runningExec) SetExited(int)                            {}

var errAlreadyStarted = errors.New("cannot start a running process")

func TestSecondExecStartDoesNotRecordPidTwice(t *testing.T) {
	const pid = 42
	ctx, sd := shutdown.WithShutdown(context.Background())
	defer sd.Shutdown()

	container := &runc.Container{ID: "ctr"}
	exec := &runningExec{id: "exec", pid: pid}
	container.ProcessAdd(exec)

	s := &service{
		context:           ctx,
		events:            make(chan any, 4),
		ec:                make(chan runcC.Exit),
		containers:        map[string]*runc.Container{"ctr": container},
		running:           map[int][]containerProcess{},
		runningExecs:      map[*runc.Container]int{container: 1},
		shutdown:          sd,
		exitSubscribers:   map[*map[int][]runcC.Exit]struct{}{},
		containerInitExit: map[*runc.Container]runcC.Exit{},
	}
	s.running[pid] = []containerProcess{{
		Container: container,
		Process:   exec,
	}}

	_, err := s.Start(ctx, &taskAPI.StartRequest{ID: "ctr", ExecID: "exec"})
	if err == nil {
		t.Fatal("second start returned nil, want an error")
	}
	if got := len(s.running[pid]); got != 1 {
		t.Fatalf("running entries for pid %d = %d, want 1", pid, got)
	}
	if got := s.runningExecs[container]; got != 1 {
		t.Fatalf("runningExecs = %d, want 1", got)
	}

	s.lifecycleMu.Lock()
	var exited []containerProcess
	for _, cp := range s.running[pid] {
		exited = append(exited, cp)
	}
	delete(s.running, pid)
	s.lifecycleMu.Unlock()
	for _, cp := range exited {
		s.handleProcessExit(runcC.Exit{Pid: pid, Status: 1}, cp.Container, cp.Process)
	}
	if got := s.runningExecs[container]; got != 0 {
		t.Fatalf("runningExecs after the exec exits = %d, want 0", got)
	}
}
