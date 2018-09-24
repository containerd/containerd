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

package windows

import (
	"context"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/windows/hcsshimtypes"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

type task struct {
	sync.Mutex

	id        string
	namespace string
	pid       uint32
	io        *pipeSet
	status    runtime.Status
	spec      *specs.Spec
	processes map[string]*process
	hyperV    bool

	publisher     events.Publisher
	rwLayer       string
	mountLocation string
	rootfs        []mount.Mount

	pidPool           *pidPool
	hcsContainer      hcsshim.Container
	terminateDuration time.Duration
	tasks             *runtime.TaskList
}

func (t *task) ID() string {
	return t.id
}

func (t *task) Namespace() string {
	return t.namespace
}

func (t *task) State(ctx context.Context) (runtime.State, error) {
	var (
		status     runtime.Status
		exitStatus uint32
		exitedAt   time.Time
	)

	if p := t.getProcess(t.id); p != nil {
		status = p.Status()
		exitStatus = p.exitCode
		exitedAt = p.exitTime
	} else {
		status = t.getStatus()
	}

	return runtime.State{
		Status:     status,
		Pid:        t.pid,
		Stdin:      t.io.src.Stdin,
		Stdout:     t.io.src.Stdout,
		Stderr:     t.io.src.Stderr,
		Terminal:   t.io.src.Terminal,
		ExitStatus: exitStatus,
		ExitedAt:   exitedAt,
	}, nil
}

func (t *task) Kill(ctx context.Context, signal uint32, all bool) error {
	p := t.getProcess(t.id)
	if p == nil {
		return errors.Wrapf(errdefs.ErrFailedPrecondition, "task is not running")
	}

	if p.Status() == runtime.StoppedStatus {
		return errors.Wrapf(errdefs.ErrNotFound, "process is stopped")
	}

	return p.Kill(ctx, signal, all)
}

func (t *task) ResizePty(ctx context.Context, size runtime.ConsoleSize) error {
	p := t.getProcess(t.id)
	if p == nil {
		return errors.Wrap(errdefs.ErrFailedPrecondition, "task not started")
	}

	return p.ResizePty(ctx, size)
}

func (t *task) CloseIO(ctx context.Context) error {
	p := t.getProcess(t.id)
	if p == nil {
		return errors.Wrap(errdefs.ErrFailedPrecondition, "task not started")
	}

	return p.hcs.CloseStdin()
}

func (t *task) Info() runtime.TaskInfo {
	return runtime.TaskInfo{
		ID:        t.id,
		Runtime:   pluginID,
		Namespace: t.namespace,
		// TODO(mlaventure): what about Spec? I think this could be removed from the info, the id is enough since it matches the one from the container
	}
}

func (t *task) Start(ctx context.Context) error {
	p := t.getProcess(t.id)
	if p == nil {
		panic("init process is missing")
	}

	if p.Status() != runtime.CreatedStatus {
		return errors.Wrap(errdefs.ErrFailedPrecondition, "process was already started")
	}

	if err := p.Start(ctx); err != nil {
		return err
	}
	t.publisher.Publish(ctx,
		runtime.TaskStartEventTopic,
		&eventstypes.TaskStart{
			ContainerID: t.id,
			Pid:         t.pid,
		})
	return nil
}

func (t *task) Pause(ctx context.Context) error {
	if t.hyperV {
		err := t.hcsContainer.Pause()
		if err == nil {
			t.Lock()
			t.status = runtime.PausedStatus
			t.Unlock()

			t.publisher.Publish(ctx,
				runtime.TaskPausedEventTopic,
				&eventstypes.TaskPaused{
					ContainerID: t.id,
				})
			return nil
		}
		return errors.Wrap(err, "hcsshim failed to pause task")
	}

	return errors.Wrap(errdefs.ErrFailedPrecondition, "not an hyperV task")
}

func (t *task) Resume(ctx context.Context) error {
	if t.hyperV {
		err := t.hcsContainer.Resume()
		if err == nil {
			t.Lock()
			t.status = runtime.RunningStatus
			t.Unlock()

			t.publisher.Publish(ctx,
				runtime.TaskResumedEventTopic,
				&eventstypes.TaskResumed{
					ContainerID: t.id,
				})
			return nil
		}
		return errors.Wrap(err, "hcsshim failed to resume task")
	}

	return errors.Wrap(errdefs.ErrFailedPrecondition, "not an hyperV task")
}

func (t *task) Exec(ctx context.Context, id string, opts runtime.ExecOpts) (runtime.Process, error) {
	if p := t.getProcess(t.id); p == nil {
		return nil, errors.Wrap(errdefs.ErrFailedPrecondition, "task not started")
	}

	if p := t.getProcess(id); p != nil {
		return nil, errors.Wrap(errdefs.ErrAlreadyExists, "id already in use")
	}

	s, err := typeurl.UnmarshalAny(opts.Spec)
	if err != nil {
		return nil, err
	}
	spec := s.(*specs.Process)
	if spec.Cwd == "" {
		spec.Cwd = t.spec.Process.Cwd
	}

	var pset *pipeSet
	if pset, err = newPipeSet(ctx, opts.IO); err != nil {
		return nil, err
	}

	conf := newWindowsProcessConfig(spec, pset)
	p, err := t.newProcess(ctx, id, conf, pset)
	if err != nil {
		return nil, err
	}

	t.publisher.Publish(ctx,
		runtime.TaskExecAddedEventTopic,
		&eventstypes.TaskExecAdded{
			ContainerID: t.id,
			ExecID:      id,
		})

	return p, nil
}

func (t *task) Pids(ctx context.Context) ([]runtime.ProcessInfo, error) {
	t.Lock()
	defer t.Unlock()

	var processList []runtime.ProcessInfo
	hcsProcessList, err := t.hcsContainer.ProcessList()
	if err != nil {
		return nil, err
	}

	for _, hcsProcess := range hcsProcessList {
		info := &hcsshimtypes.ProcessDetails{
			ImageName:                    hcsProcess.ImageName,
			CreatedAt:                    hcsProcess.CreateTimestamp,
			KernelTime_100Ns:             hcsProcess.KernelTime100ns,
			MemoryCommitBytes:            hcsProcess.MemoryCommitBytes,
			MemoryWorkingSetPrivateBytes: hcsProcess.MemoryWorkingSetPrivateBytes,
			MemoryWorkingSetSharedBytes:  hcsProcess.MemoryWorkingSetSharedBytes,
			ProcessID:                    hcsProcess.ProcessId,
			UserTime_100Ns:               hcsProcess.UserTime100ns,
		}
		for _, p := range t.processes {
			if p.HcsPid() == hcsProcess.ProcessId {
				info.ExecID = p.ID()
				break
			}
		}
		processList = append(processList, runtime.ProcessInfo{
			Pid:  hcsProcess.ProcessId,
			Info: info,
		})
	}

	return processList, nil
}

func (t *task) Checkpoint(_ context.Context, _ string, _ *types.Any) error {
	return errors.Wrap(errdefs.ErrUnavailable, "not supported")
}

func (t *task) Update(ctx context.Context, resources *types.Any) error {
	return errors.Wrap(errdefs.ErrUnavailable, "not supported")
}

func (t *task) Process(ctx context.Context, id string) (p runtime.Process, err error) {
	p = t.getProcess(id)
	if p == nil {
		err = errors.Wrapf(errdefs.ErrNotFound, "no such process %s", id)
	}

	return p, err
}

func (t *task) Stats(ctx context.Context) (*types.Any, error) {
	return nil, errors.Wrap(errdefs.ErrUnavailable, "not supported")
}

func (t *task) Wait(ctx context.Context) (*runtime.Exit, error) {
	p := t.getProcess(t.id)
	if p == nil {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "no such process %s", t.id)
	}
	return p.Wait(ctx)
}

func (t *task) Delete(ctx context.Context) (*runtime.Exit, error) {
	var (
		err      error
		state, _ = t.State(ctx)
	)
	switch state.Status {
	case runtime.StoppedStatus:
		fallthrough
	case runtime.CreatedStatus:
		// if it's stopped or in created state, we need to shutdown the
		// container before removing it
		if err = t.stop(ctx); err != nil {
			return nil, err
		}
	default:
		return nil, errors.Wrap(errdefs.ErrFailedPrecondition,
			"cannot delete a non-stopped task")
	}

	var rtExit *runtime.Exit
	if p := t.getProcess(t.ID()); p != nil {
		ec, ea, err := p.ExitCode()
		if err != nil {
			return nil, err
		}
		rtExit = &runtime.Exit{
			Pid:       t.pid,
			Status:    ec,
			Timestamp: ea,
		}
	} else {
		rtExit = &runtime.Exit{
			Pid:       t.pid,
			Status:    255,
			Timestamp: time.Now().UTC(),
		}
	}

	t.cleanup()
	t.tasks.Delete(ctx, t.ID())

	t.publisher.Publish(ctx,
		runtime.TaskDeleteEventTopic,
		&eventstypes.TaskDelete{
			ContainerID: t.id,
			Pid:         t.pid,
			ExitStatus:  rtExit.Status,
			ExitedAt:    rtExit.Timestamp,
		})

	if err := mount.UnmountAll(t.rootfs[0].Source, 0); err != nil {
		log.G(ctx).WithError(err).WithField("path", t.rootfs[0].Source).
			Warn("failed to unmount rootfs on failure")
	}
	// We were never started, return failure
	return rtExit, nil
}

func (t *task) newProcess(ctx context.Context, id string, conf *hcsshim.ProcessConfig, pset *pipeSet) (*process, error) {
	var (
		err error
		pid uint32
	)

	// If we fail, close the io right now
	defer func() {
		if err != nil {
			pset.Close()
		}
	}()

	t.Lock()
	if len(t.processes) == 0 {
		pid = t.pid
	} else {
		if pid, err = t.pidPool.Get(); err != nil {
			t.Unlock()
			return nil, err
		}
		defer func() {
			if err != nil {
				t.pidPool.Put(pid)
			}
		}()
	}
	wp := &process{
		id:     id,
		pid:    pid,
		io:     pset,
		task:   t,
		exitCh: make(chan struct{}),
		conf:   conf,
	}
	t.processes[id] = wp
	t.Unlock()

	return wp, nil
}

func (t *task) getProcess(id string) *process {
	t.Lock()
	p := t.processes[id]
	t.Unlock()

	return p
}

func (t *task) removeProcessNL(id string) {
	if p, ok := t.processes[id]; ok {
		if p.io != nil {
			p.io.Close()
		}
		t.pidPool.Put(p.pid)
		delete(t.processes, id)
	}
}

func (t *task) removeProcess(id string) {
	t.Lock()
	t.removeProcessNL(id)
	t.Unlock()
}

func (t *task) getStatus() runtime.Status {
	t.Lock()
	status := t.status
	t.Unlock()

	return status
}

// stop tries to shutdown the task.
// It will do so by first calling Shutdown on the hcsshim.Container and if
// that fails, by resorting to caling Terminate
func (t *task) stop(ctx context.Context) error {
	if err := t.hcsStop(ctx, t.hcsContainer.Shutdown); err != nil {
		return t.hcsStop(ctx, t.hcsContainer.Terminate)
	}
	t.hcsContainer.Close()
	return nil
}

func (t *task) hcsStop(ctx context.Context, stop func() error) error {
	err := stop()
	switch {
	case hcsshim.IsPending(err):
		err = t.hcsContainer.WaitTimeout(t.terminateDuration)
	case hcsshim.IsAlreadyStopped(err):
		err = nil
	}
	return err
}

func (t *task) cleanup() {
	t.Lock()
	for _, p := range t.processes {
		t.removeProcessNL(p.id)
	}
	t.Unlock()
}
