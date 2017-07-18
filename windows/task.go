// +build windows

package windows

import (
	"context"
	"io"
	"sync"
	"syscall"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/Sirupsen/logrus"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/typeurl"
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

	emitter *events.Emitter
	rwLayer string

	pidPool           *pidPool
	hcsContainer      hcsshim.Container
	terminateDuration time.Duration
	servicing         bool
}

func (t *task) ID() string {
	return t.id
}

func (t *task) State(ctx context.Context) (runtime.State, error) {
	var status runtime.Status

	if p := t.getProcess(t.id); p != nil {
		status = p.Status()
	} else {
		status = t.getStatus()
	}

	return runtime.State{
		Status:   status,
		Pid:      t.pid,
		Stdin:    t.io.src.Stdin,
		Stdout:   t.io.src.Stdout,
		Stderr:   t.io.src.Stderr,
		Terminal: t.io.src.Terminal,
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
	conf := newProcessConfig(t.spec.Process, t.io)
	if _, err := t.newProcess(ctx, t.id, conf, t.io); err != nil {
		return err
	}

	t.emitter.Post(events.WithTopic(ctx, "/tasks/start"), &eventsapi.TaskStart{
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
		}
		if err == nil {
			t.emitter.Post(events.WithTopic(ctx, "/tasks/paused"), &eventsapi.TaskPaused{
				ContainerID: t.id,
			})
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
		}
		if err == nil {
			t.emitter.Post(events.WithTopic(ctx, "/tasks/resumed"), &eventsapi.TaskResumed{
				ContainerID: t.id,
			})
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
	defer func() {
		if err != nil {
			pset.Close()
		}
	}()

	conf := newProcessConfig(spec, pset)
	p, err := t.newProcess(ctx, id, conf, pset)
	if err != nil {
		return nil, err
	}

	t.emitter.Post(events.WithTopic(ctx, "/tasks/exec-added"), &eventsapi.TaskExecAdded{
		ContainerID: t.id,
		ExecID:      id,
		Pid:         p.Pid(),
	})

	return p, nil
}

func (t *task) Pids(ctx context.Context) ([]uint32, error) {
	t.Lock()
	defer t.Unlock()

	var (
		pids = make([]uint32, len(t.processes))
		idx  = 0
	)
	for _, p := range t.processes {
		pids[idx] = p.Pid()
		idx++
	}

	return pids, nil
}

func (t *task) Checkpoint(_ context.Context, _ string, _ *types.Any) error {
	return errors.Wrap(errdefs.ErrUnavailable, "not supported")
}

func (t *task) DeleteProcess(ctx context.Context, id string) (*runtime.Exit, error) {
	if id == t.id {
		return nil, errors.Wrapf(errdefs.ErrInvalidArgument,
			"cannot delete init process")
	}
	if p := t.getProcess(id); p != nil {
		ec, ea, err := p.ExitCode()
		if err != nil {
			return nil, err
		}
		t.removeProcess(id)
		return &runtime.Exit{
			Pid:       p.pid,
			Status:    ec,
			Timestamp: ea,
		}, nil
	}
	return nil, errors.Wrapf(errdefs.ErrNotFound, "no such process %s", id)
}

func (t *task) Update(ctx context.Context, resources *types.Any) error {
	return errors.Wrap(errdefs.ErrUnavailable, "not supported")
}

func (t *task) Process(ctx context.Context, id string) (p runtime.Process, err error) {
	p = t.getProcess(id)
	if p == nil {
		err = errors.Wrapf(errdefs.ErrNotFound, "no such process %d", id)
	}

	return p, err
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
	t.Unlock()

	var p hcsshim.Process
	if p, err = t.hcsContainer.CreateProcess(conf); err != nil {
		return nil, errors.Wrapf(err, "failed to create process")
	}

	stdin, stdout, stderr, err := p.Stdio()
	if err != nil {
		p.Kill()
		return nil, errors.Wrapf(err, "failed to retrieve init process stdio")
	}

	ioCopy := func(name string, dst io.WriteCloser, src io.ReadCloser) {
		log.G(ctx).WithFields(logrus.Fields{"id": id, "pid": pid}).
			Debugf("%s: copy started", name)
		io.Copy(dst, src)
		log.G(ctx).WithFields(logrus.Fields{"id": id, "pid": pid}).
			Debugf("%s: copy done", name)
		dst.Close()
		src.Close()
	}

	if pset.stdin != nil {
		go ioCopy("stdin", stdin, pset.stdin)
	}

	if pset.stdout != nil {
		go ioCopy("stdout", pset.stdout, stdout)
	}

	if pset.stderr != nil {
		go ioCopy("stderr", pset.stderr, stderr)
	}

	t.Lock()
	wp := &process{
		id:     id,
		pid:    pid,
		io:     pset,
		status: runtime.RunningStatus,
		task:   t,
		hcs:    p,
		exitCh: make(chan struct{}),
	}
	t.processes[id] = wp
	t.Unlock()

	// Wait for the process to exit to get the exit status
	go func() {
		if err := p.Wait(); err != nil {
			herr, ok := err.(*hcsshim.ProcessError)
			if ok && herr.Err != syscall.ERROR_BROKEN_PIPE {
				log.G(ctx).
					WithError(err).
					WithFields(logrus.Fields{"id": id, "pid": pid}).
					Warnf("hcsshim wait failed (process may have been killed)")
			}
			// Try to get the exit code nonetheless
		}
		wp.exitTime = time.Now()

		ec, err := p.ExitCode()
		if err != nil {
			log.G(ctx).
				WithError(err).
				WithFields(logrus.Fields{"id": id, "pid": pid}).
				Warnf("hcsshim could not retrieve exit code")
			// Use the unknown exit code
			ec = 255
		}
		wp.exitCode = uint32(ec)

		t.emitter.Post(events.WithTopic(ctx, "/tasks/exit"), &eventsapi.TaskExit{
			ContainerID: t.id,
			ID:          id,
			Pid:         pid,
			ExitStatus:  wp.exitCode,
			ExitedAt:    wp.exitTime,
		})

		close(wp.exitCh)
		// Ensure io's are closed
		pset.Close()
		// Cleanup HCS resources
		p.Close()
	}()

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
	removeLayer(context.Background(), t.rwLayer)
	t.Unlock()
}
