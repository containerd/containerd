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
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/containerd/console"
	eventstypes "github.com/containerd/containerd/api/events"
	taskAPI "github.com/containerd/containerd/api/runtime/task/v3"
	runcC "github.com/containerd/go-runc"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/runc"
	oomv2 "github.com/containerd/containerd/v2/internal/oom"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"github.com/containerd/containerd/v2/pkg/sys"
)

func TestHandleStartedIgnoresDuplicateEarlyExitsForInit(t *testing.T) {
	s := newTestService()
	initProc := newTestInit(t, "init", 1234)
	container := newTestContainer(t, "init-ctr", initProc)

	s.running[initProc.Pid()] = []containerProcess{{
		Container: container,
		Process:   initProc,
		pidfd:     invalidPidFD,
	}}

	s.lifecycleMu.Lock()
	handleStarted, cleanup := s.preStart(container)
	s.lifecycleMu.Unlock()
	t.Cleanup(cleanup)

	_, stillRunning := s.running[initProc.Pid()]
	require.False(t, stillRunning, "preStart should remove the init from running while Start is in flight")

	injectEarlyExits(s, initProc.Pid(),
		runcC.Exit{Pid: initProc.Pid(), Status: 1, Timestamp: time.Unix(1, 0)},
		runcC.Exit{Pid: initProc.Pid(), Status: 2, Timestamp: time.Unix(2, 0)},
	)

	handleStarted(container, initProc)

	exit := readTaskExit(t, s)
	require.Equal(t, container.ID, exit.ContainerID)
	require.Equal(t, "init", exit.ID)
	require.Equal(t, uint32(initProc.Pid()), exit.Pid)
	require.Equal(t, uint32(1), exit.ExitStatus)
	require.Equal(t, 1, initProc.ExitStatus(), "stored exit status should remain the first exit")
	require.Equal(t, []string{container.ID}, s.cg2oom.(*fakeOOM).stopped)
	assertNoMoreEvents(t, s)
}

func TestHandleStartedIgnoresDuplicateEarlyExitsForExec(t *testing.T) {
	s := newTestService()
	container := newTestContainer(t, "exec-ctr", newTestInit(t, "init", 2222))
	execProc := &fakeProcess{id: "exec1", pid: 3333}
	container.ProcessAdd(execProc)
	s.containers[container.ID] = container

	s.runningExecs[container] = 1

	s.lifecycleMu.Lock()
	handleStarted, cleanup := s.preStart(nil)
	s.lifecycleMu.Unlock()
	t.Cleanup(cleanup)

	injectEarlyExits(s, execProc.Pid(),
		runcC.Exit{Pid: execProc.Pid(), Status: 1, Timestamp: time.Unix(3, 0)},
		runcC.Exit{Pid: execProc.Pid(), Status: 2, Timestamp: time.Unix(4, 0)},
	)

	handleStarted(container, execProc)

	exit := readTaskExit(t, s)
	require.Equal(t, container.ID, exit.ContainerID)
	require.Equal(t, execProc.id, exit.ID)
	require.Equal(t, uint32(execProc.Pid()), exit.Pid)
	require.Equal(t, uint32(1), exit.ExitStatus)
	require.Equal(t, 1, execProc.status, "stored exit status should remain the first exit")
	require.Equal(t, 1, execProc.setExitedCalls, "duplicate exits must not call SetExited twice")
	require.Equal(t, 0, s.runningExecs[container], "duplicate exits must not double-decrement running execs")

	state, err := s.State(context.Background(), &taskAPI.StateRequest{
		ID:     container.ID,
		ExecID: execProc.id,
	})
	require.NoError(t, err)
	require.Equal(t, uint32(1), state.ExitStatus, "State should expose the first early exit")

	resp, err := s.Delete(context.Background(), &taskAPI.DeleteRequest{
		ID:     container.ID,
		ExecID: execProc.id,
	})
	require.NoError(t, err)
	require.Equal(t, uint32(1), resp.ExitStatus, "Delete should expose the first early exit")
	assertNoMoreEvents(t, s)
}

func TestProcessExitsSkipsLivePidFDForRecycledInit(t *testing.T) {
	if !sys.SupportsPidFD() {
		t.Skip("pidfd not supported")
	}

	s := newTestService()
	initProc := newTestInit(t, "init", os.Getpid())
	container := newTestContainer(t, "live-pidfd-init-ctr", initProc)
	pidfd, err := openPidFD(os.Getpid())
	if err != nil || pidfd == invalidPidFD {
		t.Skip("pidfd_open failed")
	}
	t.Cleanup(func() {
		closePidFD(pidfd)
	})

	s.containers[container.ID] = container
	s.running[initProc.Pid()] = []containerProcess{{
		Container: container,
		Process:   initProc,
		pidfd:     pidfd,
	}}

	runProcessExitsOnce(t, s, runcC.Exit{
		Pid:       initProc.Pid(),
		Status:    2,
		Timestamp: time.Unix(31, 0),
	})

	require.Equal(t, 0, initProc.ExitStatus(), "live pidfd must prevent attribution to a newer init process with the same PID")
	_, initExited := s.containerInitExit[container]
	require.False(t, initExited, "live pidfd must prevent recording an init exit")
	require.Contains(t, s.running, initProc.Pid(), "live pidfd must keep the newer init tracked")
	assertNoMoreEvents(t, s)
}

func TestProcessExitsSkipsLivePidFDForRecycledExec(t *testing.T) {
	if !sys.SupportsPidFD() {
		t.Skip("pidfd not supported")
	}

	s := newTestService()
	container := newTestContainer(t, "live-pidfd-exec-ctr", newTestInit(t, "init", 5555))
	pidfd, err := openPidFD(os.Getpid())
	if err != nil || pidfd == invalidPidFD {
		t.Skip("pidfd_open failed")
	}
	t.Cleanup(func() {
		closePidFD(pidfd)
	})

	execProc := &fakeProcess{id: "exec1", pid: os.Getpid()}
	container.ProcessAdd(execProc)

	s.containers[container.ID] = container
	s.running[execProc.Pid()] = []containerProcess{{
		Container: container,
		Process:   execProc,
		pidfd:     pidfd,
	}}
	s.runningExecs[container] = 1

	runProcessExitsOnce(t, s, runcC.Exit{
		Pid:       execProc.Pid(),
		Status:    2,
		Timestamp: time.Unix(41, 0),
	})

	require.Equal(t, 0, execProc.status, "live pidfd must prevent attribution to a newer process with the same PID")
	require.Equal(t, 0, execProc.setExitedCalls, "live pidfd must prevent SetExited")
	require.Equal(t, 1, s.runningExecs[container], "live pidfd must prevent runningExecs decrement")
	require.Contains(t, s.running, execProc.Pid(), "live pidfd must keep the newer process tracked")
	assertNoMoreEvents(t, s)
}

// This is a characterization test for the remaining PID-only lookup in
// processExits. It shows that once a process is in s.running, an exit is
// attributed purely by PID and becomes visible through State().
func TestProcessExitsMatchesRunningInitByPIDOnly(t *testing.T) {
	s := newTestService()
	initProc := newTestInit(t, "init", 4444)
	container := newTestContainer(t, "running-init-ctr", initProc)
	container.Bundle = newPrivatePIDBundle(t)

	s.containers[container.ID] = container
	s.running[initProc.Pid()] = []containerProcess{{
		Container: container,
		Process:   initProc,
		pidfd:     invalidPidFD,
	}}

	runProcessExitsOnce(t, s, runcC.Exit{
		Pid:       initProc.Pid(),
		Status:    2,
		Timestamp: time.Unix(6, 0),
	})

	exit := readTaskExit(t, s)
	require.Equal(t, container.ID, exit.ContainerID)
	require.Equal(t, uint32(2), exit.ExitStatus)
	require.Equal(t, 2, initProc.ExitStatus(), "the tracked init process inherits the exit solely by PID")
	require.Equal(t, 2, s.containerInitExit[container].Status)
	require.Equal(t, []string{container.ID}, s.cg2oom.(*fakeOOM).stopped)

	state, err := s.State(context.Background(), &taskAPI.StateRequest{ID: container.ID})
	require.NoError(t, err)
	require.Equal(t, uint32(2), state.ExitStatus)
	assertNoMoreEvents(t, s)
}

func TestCloseTrackedPidFDs(t *testing.T) {
	if !sys.SupportsPidFD() {
		t.Skip("pidfd not supported")
	}

	s := newTestService()
	execProc := &fakeProcess{id: "exec1", pid: os.Getpid()}
	pidfd, err := openPidFD(execProc.Pid())
	if err != nil || pidfd == invalidPidFD {
		t.Skip("pidfd_open failed")
	}

	s.running[execProc.Pid()] = []containerProcess{{
		Container: newTestContainer(t, "cleanup-ctr", newTestInit(t, "init", 33333)),
		Process:   execProc,
		pidfd:     pidfd,
	}}

	isLive, ok := pidFDIsRunning(pidfd)
	require.True(t, ok)
	require.True(t, isLive)

	s.closeTrackedPidFDs()

	require.Equal(t, invalidPidFD, s.running[execProc.Pid()][0].pidfd)
	isLive, ok = pidFDIsRunning(pidfd)
	require.False(t, ok)
	require.False(t, isLive)
}

func newTestService() *service {
	return &service{
		context:              context.Background(),
		events:               make(chan any, 8),
		cg2oom:               &fakeOOM{},
		containers:           make(map[string]*runc.Container),
		running:              make(map[int][]containerProcess),
		runningExecs:         make(map[*runc.Container]int),
		execCountSubscribers: make(map[*runc.Container]chan<- int),
		containerInitExit:    make(map[*runc.Container]runcC.Exit),
		exitSubscribers:      make(map[*map[int][]runcC.Exit]struct{}),
	}
}

func newTestInit(t *testing.T, id string, pid int) *process.Init {
	t.Helper()

	p := process.New(id, nil, stdio.Stdio{})
	p.Platform = fakePlatform{}
	setUnexportedField(t, p, "pid", pid)
	return p
}

func newTestContainer(t *testing.T, id string, initProc *process.Init) *runc.Container {
	t.Helper()

	c := &runc.Container{ID: id}
	setUnexportedField(t, c, "process", process.Process(initProc))
	setUnexportedField(t, c, "processes", make(map[string]process.Process))
	setUnexportedField(t, c, "reservedProcess", make(map[string]struct{}))
	return c
}

func newPrivatePIDBundle(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"linux":{"namespaces":[{"type":"pid"}]}}`), 0o644)
	require.NoError(t, err)
	return dir
}

func injectEarlyExits(s *service, pid int, exits ...runcC.Exit) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	for subscriber := range s.exitSubscribers {
		(*subscriber)[pid] = append((*subscriber)[pid], exits...)
	}
}

func runProcessExitsOnce(t *testing.T, s *service, exit runcC.Exit) {
	t.Helper()

	s.ec = make(chan runcC.Exit, 1)
	done := make(chan struct{})
	go func() {
		s.processExits()
		close(done)
	}()

	s.ec <- exit
	close(s.ec)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for processExits")
	}
}

func readTaskExit(t *testing.T, s *service) *eventstypes.TaskExit {
	t.Helper()

	select {
	case evt := <-s.events:
		exit, ok := evt.(*eventstypes.TaskExit)
		require.True(t, ok, "expected TaskExit, got %T", evt)
		return exit
	default:
		t.Fatal("expected TaskExit event")
		return nil
	}
}

func assertNoMoreEvents(t *testing.T, s *service) {
	t.Helper()

	select {
	case evt := <-s.events:
		t.Fatalf("unexpected extra event: %#v", evt)
	default:
	}
}

func setUnexportedField(t *testing.T, target any, field string, value any) {
	t.Helper()

	v := reflect.ValueOf(target)
	require.Equal(t, reflect.Ptr, v.Kind(), "target must be a pointer")

	f := v.Elem().FieldByName(field)
	require.True(t, f.IsValid(), "missing field %q", field)

	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

type fakePlatform struct{}

func (fakePlatform) CopyConsole(context.Context, console.Console, string, string, string, string, *sync.WaitGroup) (console.Console, error) {
	return nil, nil
}

func (fakePlatform) ShutdownConsole(context.Context, console.Console) error {
	return nil
}

func (fakePlatform) Close() error {
	return nil
}

type fakeOOM struct {
	stopped []string
}

func (f *fakeOOM) Add(string, int, oomv2.EventFunc) error {
	return nil
}

func (f *fakeOOM) Stop(containerID string) error {
	f.stopped = append(f.stopped, containerID)
	return nil
}

type fakeProcess struct {
	id             string
	pid            int
	status         int
	exitedAt       time.Time
	exited         bool
	setExitedCalls int
}

func (p *fakeProcess) ID() string {
	return p.id
}

func (p *fakeProcess) Pid() int {
	return p.pid
}

func (p *fakeProcess) ExitStatus() int {
	return p.status
}

func (p *fakeProcess) ExitedAt() time.Time {
	return p.exitedAt
}

func (p *fakeProcess) Stdin() io.Closer {
	return nil
}

func (p *fakeProcess) Stdio() stdio.Stdio {
	return stdio.Stdio{}
}

func (p *fakeProcess) Status(context.Context) (string, error) {
	if p.exited {
		return "stopped", nil
	}
	return "created", nil
}

func (p *fakeProcess) Wait(context.Context) error {
	return nil
}

func (p *fakeProcess) Resize(console.WinSize) error {
	return nil
}

func (p *fakeProcess) Start(context.Context) error {
	return nil
}

func (p *fakeProcess) Delete(context.Context) error {
	return nil
}

func (p *fakeProcess) Kill(context.Context, uint32, bool) error {
	return nil
}

func (p *fakeProcess) SetExited(status int) {
	p.setExitedCalls++
	if p.exited {
		return
	}
	p.status = status
	p.exited = true
	p.exitedAt = time.Unix(5, 0)
}
