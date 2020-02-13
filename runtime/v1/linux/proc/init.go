// +build !windows

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

package proc

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

	"github.com/containerd/console"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/runtime/proc"
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
	google_protobuf "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

// InitPidFile name of the file that contains the init pid
const InitPidFile = "init.pid"

// Init represents an initial process for a container
type Init struct {
	wg        sync.WaitGroup
	initState initState

	// mu is used to ensure that `Start()` and `Exited()` calls return in
	// the right order when invoked in separate go routines.
	// This is the case within the shim implementation as it makes use of
	// the reaper interface.
	mu sync.Mutex

	waitBlock chan struct{}

	WorkDir string

	id       string
	Bundle   string
	console  console.Console
	Platform proc.Platform
	io       runc.IO
	runtime  *runc.Runc
	// pausing preserves the pausing state.
	pausing      *atomicBool
	status       int
	exited       time.Time
	pid          int
	closers      []io.Closer
	stdin        io.Closer
	stdio        proc.Stdio
	Rootfs       string
	IoUID        int
	IoGID        int
	NoPivotRoot  bool
	NoNewKeyring bool
}

// NewRunc returns a new runc instance for a process
func NewRunc(root, path, namespace, runtime, criu string, systemd bool) *runc.Runc {
	if root == "" {
		root = RuncRoot
	}
	return &runc.Runc{
		Command:       runtime,
		Log:           filepath.Join(path, "log.json"),
		LogFormat:     runc.JSON,
		PdeathSignal:  syscall.SIGKILL,
		Root:          filepath.Join(root, namespace),
		Criu:          criu,
		SystemdCgroup: systemd,
	}
}

// New returns a new process
func New(id string, runtime *runc.Runc, stdio proc.Stdio) *Init {
	p := &Init{
		id:        id,
		runtime:   runtime,
		pausing:   new(atomicBool),
		stdio:     stdio,
		status:    0,
		waitBlock: make(chan struct{}),
	}
	p.initState = &createdState{p: p}
	return p
}

// Create the process with the provided config
func (p *Init) Create(ctx context.Context, r *CreateConfig) error {
	var (
		err    error
		socket *runc.Socket
	)
	if r.Terminal {
		if socket, err = runc.NewTempConsoleSocket(); err != nil {
			return errors.Wrap(err, "failed to create OCI runtime console socket")
		}
		defer socket.Close()
	} else if hasNoIO(r) {
		if p.io, err = runc.NewNullIO(); err != nil {
			return errors.Wrap(err, "creating new NULL IO")
		}
	} else {
		if p.io, err = runc.NewPipeIO(p.IoUID, p.IoGID, withConditionalIO(p.stdio)); err != nil {
			return errors.Wrap(err, "failed to create OCI runtime io pipes")
		}
	}
	pidFile := filepath.Join(p.Bundle, InitPidFile)
	if r.Checkpoint != "" {
		opts := &runc.RestoreOpts{
			CheckpointOpts: runc.CheckpointOpts{
				ImagePath:  r.Checkpoint,
				WorkDir:    p.WorkDir,
				ParentPath: r.ParentCheckpoint,
			},
			PidFile:     pidFile,
			IO:          p.io,
			NoPivot:     p.NoPivotRoot,
			Detach:      true,
			NoSubreaper: true,
		}
		p.initState = &createdCheckpointState{
			p:    p,
			opts: opts,
		}
		return nil
	}
	opts := &runc.CreateOpts{
		PidFile:      pidFile,
		IO:           p.io,
		NoPivot:      p.NoPivotRoot,
		NoNewKeyring: p.NoNewKeyring,
	}
	if socket != nil {
		opts.ConsoleSocket = socket
	}
	if err := p.runtime.Create(ctx, r.ID, r.Bundle, opts); err != nil {
		return p.runtimeError(err, "OCI runtime create failed")
	}
	if r.Stdin != "" {
		sc, err := fifo.OpenFifo(context.Background(), r.Stdin, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return errors.Wrapf(err, "failed to open stdin fifo %s", r.Stdin)
		}
		p.stdin = sc
		p.closers = append(p.closers, sc)
	}
	var copyWaitGroup sync.WaitGroup
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if socket != nil {
		console, err := socket.ReceiveMaster()
		if err != nil {
			return errors.Wrap(err, "failed to retrieve console master")
		}
		console, err = p.Platform.CopyConsole(ctx, console, r.Stdin, r.Stdout, r.Stderr, &p.wg, &copyWaitGroup)
		if err != nil {
			return errors.Wrap(err, "failed to start console copy")
		}
		p.console = console
	} else if !hasNoIO(r) {
		if err := copyPipes(ctx, p.io, r.Stdin, r.Stdout, r.Stderr, &p.wg, &copyWaitGroup); err != nil {
			return errors.Wrap(err, "failed to start io pipe copy")
		}
	}

	copyWaitGroup.Wait()
	pid, err := runc.ReadPidFile(pidFile)
	if err != nil {
		return errors.Wrap(err, "failed to retrieve OCI runtime container pid")
	}
	p.pid = pid
	return nil
}

// Wait for the process to exit
func (p *Init) Wait() {
	<-p.waitBlock
}

// ID of the process
func (p *Init) ID() string {
	return p.id
}

// Pid of the process
func (p *Init) Pid() int {
	return p.pid
}

// ExitStatus of the process
func (p *Init) ExitStatus() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.status
}

// ExitedAt at time when the process exited
func (p *Init) ExitedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.exited
}

// Status of the process
func (p *Init) Status(ctx context.Context) (string, error) {
	if p.pausing.get() {
		return "pausing", nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Status(ctx)
}

// Start the init process
func (p *Init) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Start(ctx)
}

func (p *Init) start(ctx context.Context) error {
	err := p.runtime.Start(ctx, p.id)
	return p.runtimeError(err, "OCI runtime start failed")
}

// SetExited of the init process with the next status
func (p *Init) SetExited(status int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.initState.SetExited(status)
}

func (p *Init) setExited(status int) {
	p.exited = time.Now()
	p.status = status
	p.Platform.ShutdownConsole(context.Background(), p.console)
	close(p.waitBlock)
}

// Delete the init process
func (p *Init) Delete(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Delete(ctx)
}

func (p *Init) delete(ctx context.Context) error {
	waitTimeout(ctx, &p.wg, 2*time.Second)
	err := p.runtime.Delete(ctx, p.id, nil)
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
	if p.Rootfs != "" {
		if err2 := mount.UnmountAll(p.Rootfs, 0); err2 != nil {
			log.G(ctx).WithError(err2).Warn("failed to cleanup rootfs mount")
			if err == nil {
				err = errors.Wrap(err2, "failed rootfs umount")
			}
		}
	}
	return err
}

// Resize the init processes console
func (p *Init) Resize(ws console.WinSize) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.console == nil {
		return nil
	}
	return p.console.Resize(ws)
}

func (p *Init) resize(ws console.WinSize) error {
	if p.console == nil {
		return nil
	}
	return p.console.Resize(ws)
}

// Pause the init process and all its child processes
func (p *Init) Pause(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Pause(ctx)
}

// Resume the init process and all its child processes
func (p *Init) Resume(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Resume(ctx)
}

// Kill the init process
func (p *Init) Kill(ctx context.Context, signal uint32, all bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Kill(ctx, signal, all)
}

func (p *Init) kill(ctx context.Context, signal uint32, all bool) error {
	err := p.runtime.Kill(ctx, p.id, int(signal), &runc.KillOpts{
		All: all,
	})
	return checkKillError(err)
}

// KillAll processes belonging to the init process
func (p *Init) KillAll(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	err := p.runtime.Kill(ctx, p.id, int(syscall.SIGKILL), &runc.KillOpts{
		All: true,
	})
	return p.runtimeError(err, "OCI runtime killall failed")
}

// Stdin of the process
func (p *Init) Stdin() io.Closer {
	return p.stdin
}

// Runtime returns the OCI runtime configured for the init process
func (p *Init) Runtime() *runc.Runc {
	return p.runtime
}

// Exec returns a new child process
func (p *Init) Exec(ctx context.Context, path string, r *ExecConfig) (proc.Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Exec(ctx, path, r)
}

// exec returns a new exec'd process
func (p *Init) exec(ctx context.Context, path string, r *ExecConfig) (proc.Process, error) {
	// process exec request
	var spec specs.Process
	if err := json.Unmarshal(r.Spec.Value, &spec); err != nil {
		return nil, err
	}
	spec.Terminal = r.Terminal

	e := &execProcess{
		id:     r.ID,
		path:   path,
		parent: p,
		spec:   spec,
		stdio: proc.Stdio{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
		waitBlock: make(chan struct{}),
		pid:       &safePid{},
	}
	e.execState = &execCreatedState{p: e}
	return e, nil
}

// Checkpoint the init process
func (p *Init) Checkpoint(ctx context.Context, r *CheckpointConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Checkpoint(ctx, r)
}

func (p *Init) checkpoint(ctx context.Context, r *CheckpointConfig) error {
	var actions []runc.CheckpointAction
	if !r.Exit {
		actions = append(actions, runc.LeaveRunning)
	}
	work := filepath.Join(p.WorkDir, "criu-work")
	defer os.RemoveAll(work)
	if err := p.runtime.Checkpoint(ctx, p.id, &runc.CheckpointOpts{
		WorkDir:                  work,
		ImagePath:                r.Path,
		AllowOpenTCP:             r.AllowOpenTCP,
		AllowExternalUnixSockets: r.AllowExternalUnixSockets,
		AllowTerminal:            r.AllowTerminal,
		FileLocks:                r.FileLocks,
		EmptyNamespaces:          r.EmptyNamespaces,
	}, actions...); err != nil {
		dumpLog := filepath.Join(p.Bundle, "criu-dump.log")
		if cerr := copyFile(dumpLog, filepath.Join(work, "dump.log")); cerr != nil {
			log.G(ctx).Error(err)
		}
		return fmt.Errorf("%s path= %s", criuError(err), dumpLog)
	}
	return nil
}

// Update the processes resource configuration
func (p *Init) Update(ctx context.Context, r *google_protobuf.Any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Update(ctx, r)
}

func (p *Init) update(ctx context.Context, r *google_protobuf.Any) error {
	var resources specs.LinuxResources
	if err := json.Unmarshal(r.Value, &resources); err != nil {
		return err
	}
	return p.runtime.Update(ctx, p.id, &resources)
}

// Stdio of the process
func (p *Init) Stdio() proc.Stdio {
	return p.stdio
}

func (p *Init) runtimeError(rErr error, msg string) error {
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

func withConditionalIO(c proc.Stdio) runc.IOOpt {
	return func(o *runc.IOOption) {
		o.OpenStdin = c.Stdin != ""
		o.OpenStdout = c.Stdout != ""
		o.OpenStderr = c.Stderr != ""
	}
}
