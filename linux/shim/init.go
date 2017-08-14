// +build !windows

package shim

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/containerd/console"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/identifiers"
	"github.com/containerd/containerd/linux/runcopts"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/typeurl"
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

type initProcess struct {
	sync.WaitGroup

	// mu is used to ensure that `Start()` and `Exited()` calls return in
	// the right order when invoked in separate go routines.
	// This is the case within the shim implementation as it makes use of
	// the reaper interface.
	mu sync.Mutex

	workDir string

	id       string
	bundle   string
	console  console.Console
	platform platform
	io       runc.IO
	runtime  *runc.Runc
	status   int
	exited   time.Time
	pid      int
	closers  []io.Closer
	stdin    io.Closer
	stdio    stdio
	rootfs   string
}

func newInitProcess(context context.Context, plat platform, path, namespace, workDir string, r *shimapi.CreateTaskRequest) (*initProcess, error) {
	var success bool

	if err := identifiers.Validate(r.ID); err != nil {
		return nil, errors.Wrapf(err, "invalid task id")
	}
	var options runcopts.CreateOptions
	if r.Options != nil {
		v, err := typeurl.UnmarshalAny(r.Options)
		if err != nil {
			return nil, err
		}
		options = *v.(*runcopts.CreateOptions)
	}

	rootfs := filepath.Join(path, "rootfs")
	// count the number of successful mounts so we can undo
	// what was actually done rather than what should have been
	// done.
	defer func() {
		if success {
			return
		}
		if err2 := mount.UnmountAll(rootfs, 0); err2 != nil {
			log.G(context).WithError(err2).Warn("Failed to cleanup rootfs mount")
		}
	}()
	for _, rm := range r.Rootfs {
		m := &mount.Mount{
			Type:    rm.Type,
			Source:  rm.Source,
			Options: rm.Options,
		}
		if err := m.Mount(rootfs); err != nil {
			return nil, errors.Wrapf(err, "failed to mount rootfs component %v", m)
		}
	}
	runtime := &runc.Runc{
		Command:      r.Runtime,
		Log:          filepath.Join(path, "log.json"),
		LogFormat:    runc.JSON,
		PdeathSignal: syscall.SIGKILL,
		Root:         filepath.Join(RuncRoot, namespace),
	}
	p := &initProcess{
		id:       r.ID,
		bundle:   r.Bundle,
		runtime:  runtime,
		platform: plat,
		stdio: stdio{
			stdin:    r.Stdin,
			stdout:   r.Stdout,
			stderr:   r.Stderr,
			terminal: r.Terminal,
		},
		rootfs:  rootfs,
		workDir: workDir,
	}
	var (
		err    error
		socket *runc.Socket
	)
	if r.Terminal {
		if socket, err = runc.NewTempConsoleSocket(); err != nil {
			return nil, errors.Wrap(err, "failed to create OCI runtime console socket")
		}
		defer socket.Close()
	} else if hasNoIO(r) {
		if p.io, err = runc.NewNullIO(); err != nil {
			return nil, errors.Wrap(err, "creating new NULL IO")
		}
	} else {
		if p.io, err = runc.NewPipeIO(); err != nil {
			return nil, errors.Wrap(err, "failed to create OCI runtime io pipes")
		}
	}
	pidFile := filepath.Join(path, "init.pid")
	if r.Checkpoint != "" {
		opts := &runc.RestoreOpts{
			CheckpointOpts: runc.CheckpointOpts{
				ImagePath:  r.Checkpoint,
				WorkDir:    p.workDir,
				ParentPath: r.ParentCheckpoint,
			},
			PidFile:     pidFile,
			IO:          p.io,
			NoPivot:     options.NoPivotRoot,
			Detach:      true,
			NoSubreaper: true,
		}
		if _, err := p.runtime.Restore(context, r.ID, r.Bundle, opts); err != nil {
			return nil, p.runtimeError(err, "OCI runtime restore failed")
		}
	} else {
		opts := &runc.CreateOpts{
			PidFile:      pidFile,
			IO:           p.io,
			NoPivot:      options.NoPivotRoot,
			NoNewKeyring: options.NoNewKeyring,
		}
		if socket != nil {
			opts.ConsoleSocket = socket
		}
		if err := p.runtime.Create(context, r.ID, r.Bundle, opts); err != nil {
			return nil, p.runtimeError(err, "OCI runtime create failed")
		}
	}
	if r.Stdin != "" {
		sc, err := fifo.OpenFifo(context, r.Stdin, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to open stdin fifo %s", r.Stdin)
		}
		p.stdin = sc
		p.closers = append(p.closers, sc)
	}
	var copyWaitGroup sync.WaitGroup
	if socket != nil {
		console, err := socket.ReceiveMaster()
		if err != nil {
			return nil, errors.Wrap(err, "failed to retrieve console master")
		}
		console, err = plat.copyConsole(context, console, r.Stdin, r.Stdout, r.Stderr, &p.WaitGroup, &copyWaitGroup)
		if err != nil {
			return nil, errors.Wrap(err, "failed to start console copy")
		}
		p.console = console
	} else if !hasNoIO(r) {
		if err := copyPipes(context, p.io, r.Stdin, r.Stdout, r.Stderr, &p.WaitGroup, &copyWaitGroup); err != nil {
			return nil, errors.Wrap(err, "failed to start io pipe copy")
		}
	}

	copyWaitGroup.Wait()
	pid, err := runc.ReadPidFile(pidFile)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve OCI runtime container pid")
	}
	p.pid = pid
	success = true
	return p, nil
}

func (p *initProcess) ID() string {
	return p.id
}

func (p *initProcess) Pid() int {
	return p.pid
}

func (p *initProcess) ExitStatus() int {
	return p.status
}

func (p *initProcess) ExitedAt() time.Time {
	return p.exited
}

// Status return the state of the container (created, running, paused, stopped)
func (p *initProcess) Status(ctx context.Context) (string, error) {
	c, err := p.runtime.State(ctx, p.id)
	if err != nil {
		return "", p.runtimeError(err, "OCI runtime state failed")
	}
	return c.Status, nil
}

func (p *initProcess) Start(context context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.runtime.Start(context, p.id)
	return p.runtimeError(err, "OCI runtime start failed")
}

func (p *initProcess) SetExited(status int) {
	p.mu.Lock()
	p.status = status
	p.exited = time.Now()
	p.platform.shutdownConsole(context.Background(), p.console)
	p.mu.Unlock()
}

func (p *initProcess) Delete(context context.Context) error {
	p.killAll(context)
	p.Wait()
	err := p.runtime.Delete(context, p.id, nil)
	// ignore errors if a runtime has already deleted the process
	// but we still hold metadata and pipes
	//
	// this is common during a checkpoint, runc will delete the container state
	// after a checkpoint and the container will no longer exist within runc
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			err = nil
		} else {
			err = p.runtimeError(err, "failed to delete task")
		}
	}
	if p.io != nil {
		for _, c := range p.closers {
			c.Close()
		}
		p.io.Close()
	}
	if err2 := mount.UnmountAll(p.rootfs, 0); err2 != nil {
		log.G(context).WithError(err2).Warn("failed to cleanup rootfs mount")
		if err == nil {
			err = errors.Wrap(err2, "failed rootfs umount")
		}
	}
	return err
}

func (p *initProcess) Resize(ws console.WinSize) error {
	if p.console == nil {
		return nil
	}
	return p.console.Resize(ws)
}

func (p *initProcess) Pause(context context.Context) error {
	err := p.runtime.Pause(context, p.id)
	return p.runtimeError(err, "OCI runtime pause failed")
}

func (p *initProcess) Resume(context context.Context) error {
	err := p.runtime.Resume(context, p.id)
	return p.runtimeError(err, "OCI runtime resume failed")
}

func (p *initProcess) Kill(context context.Context, signal uint32, all bool) error {
	err := p.runtime.Kill(context, p.id, int(signal), &runc.KillOpts{
		All: all,
	})
	return checkKillError(err)
}

func (p *initProcess) killAll(context context.Context) error {
	err := p.runtime.Kill(context, p.id, int(syscall.SIGKILL), &runc.KillOpts{
		All: true,
	})
	return p.runtimeError(err, "OCI runtime killall failed")
}

func (p *initProcess) Stdin() io.Closer {
	return p.stdin
}

func (p *initProcess) Checkpoint(context context.Context, r *shimapi.CheckpointTaskRequest) error {
	var options runcopts.CheckpointOptions
	if r.Options != nil {
		v, err := typeurl.UnmarshalAny(r.Options)
		if err != nil {
			return err
		}
		options = *v.(*runcopts.CheckpointOptions)
	}
	var actions []runc.CheckpointAction
	if !options.Exit {
		actions = append(actions, runc.LeaveRunning)
	}
	work := filepath.Join(p.workDir, "criu-work")
	defer os.RemoveAll(work)
	if err := p.runtime.Checkpoint(context, p.id, &runc.CheckpointOpts{
		WorkDir:                  p.workDir,
		ImagePath:                r.Path,
		AllowOpenTCP:             options.OpenTcp,
		AllowExternalUnixSockets: options.ExternalUnixSockets,
		AllowTerminal:            options.Terminal,
		FileLocks:                options.FileLocks,
		EmptyNamespaces:          options.EmptyNamespaces,
	}, actions...); err != nil {
		dumpLog := filepath.Join(p.bundle, "criu-dump.log")
		if cerr := copyFile(dumpLog, filepath.Join(work, "dump.log")); cerr != nil {
			log.G(context).Error(err)
		}
		return fmt.Errorf("%s path= %s", criuError(err), dumpLog)
	}
	return nil
}

func (p *initProcess) Update(context context.Context, r *shimapi.UpdateTaskRequest) error {
	var resources specs.LinuxResources
	if err := json.Unmarshal(r.Resources.Value, &resources); err != nil {
		return err
	}
	return p.runtime.Update(context, p.id, &resources)
}

func (p *initProcess) Stdio() stdio {
	return p.stdio
}

// TODO(mlaventure): move to runc package?
func getLastRuntimeError(r *runc.Runc) (string, error) {
	if r.Log == "" {
		return "", nil
	}

	f, err := os.OpenFile(r.Log, os.O_RDONLY, 0400)
	if err != nil {
		return "", err
	}

	var (
		errMsg string
		log    struct {
			Level string
			Msg   string
			Time  time.Time
		}
	)

	dec := json.NewDecoder(f)
	for err = nil; err == nil; {
		if err = dec.Decode(&log); err != nil && err != io.EOF {
			return "", err
		}
		if log.Level == "error" {
			errMsg = strings.TrimSpace(log.Msg)
		}
	}

	return errMsg, nil
}

func (p *initProcess) runtimeError(rErr error, msg string) error {
	if rErr == nil {
		return nil
	}

	rMsg, err := getLastRuntimeError(p.runtime)
	switch {
	case err != nil:
		return errors.Wrapf(rErr, "%s: %s (%s)", msg, "unable to retrieve OCI runtime error", err.Error())
	case rMsg == "":
		return errors.Wrap(rErr, msg)
	default:
		return errors.Errorf("%s: %s", msg, rMsg)
	}
}

// criuError returns only the first line of the error message from criu
// it tries to add an invalid dump log location when returning the message
func criuError(err error) string {
	parts := strings.Split(err.Error(), "\n")
	return parts[0]
}

func copyFile(to, from string) error {
	ff, err := os.Open(from)
	if err != nil {
		return err
	}
	defer ff.Close()
	tt, err := os.Create(to)
	if err != nil {
		return err
	}
	defer tt.Close()
	_, err = io.Copy(tt, ff)
	return err
}

func checkKillError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "os: process already finished") || err == unix.ESRCH {
		return errors.Wrapf(errdefs.ErrNotFound, "process already finished")
	}
	return errors.Wrapf(err, "unknown error after kill")
}

func hasNoIO(r *shimapi.CreateTaskRequest) bool {
	return r.Stdin == "" && r.Stdout == "" && r.Stderr == ""
}
