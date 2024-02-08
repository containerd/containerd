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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/stdio"
	google_protobuf "github.com/containerd/containerd/v2/protobuf/types"
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
	"github.com/containerd/log"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

// Init represents an initial process for a container
type Init struct {
	wg        sync.WaitGroup
	initState initState

	// mu is used to ensure that `Start()` and `Exited()` calls return in
	// the right order when invoked in separate goroutines.
	// This is the case within the shim implementation as it makes use of
	// the reaper interface.
	mu sync.Mutex

	waitBlock chan struct{}

	WorkDir string

	id       string
	Bundle   string
	console  console.Console
	Platform stdio.Platform
	io       *processIO
	runtime  *runc.Runc
	// pausing preserves the pausing state.
	pausing      atomic.Bool
	status       int
	exited       time.Time
	pid          int
	closers      []io.Closer
	stdin        io.Closer
	stdio        stdio.Stdio
	Rootfs       string
	IoUID        int
	IoGID        int
	NoPivotRoot  bool
	NoNewKeyring bool
	CriuWorkPath string
}

// NewRunc returns a new runc instance for a process
func NewRunc(root, path, namespace, runtime string, systemd bool) *runc.Runc {
	if root == "" {
		root = RuncRoot
	}
	return &runc.Runc{
		Command:       runtime,
		Log:           filepath.Join(path, "log.json"),
		LogFormat:     runc.JSON,
		PdeathSignal:  unix.SIGKILL,
		Root:          filepath.Join(root, namespace),
		SystemdCgroup: systemd,
	}
}

// New returns a new process
func New(id string, runtime *runc.Runc, stdio stdio.Stdio) *Init {
	p := &Init{
		id:        id,
		runtime:   runtime,
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
		err     error
		socket  *runc.Socket
		pio     *processIO
		pidFile = newPidFile(p.Bundle)
	)

	if r.Terminal {
		if socket, err = runc.NewTempConsoleSocket(); err != nil {
			return fmt.Errorf("failed to create OCI runtime console socket: %w", err)
		}
		defer socket.Close()
	} else {
		if pio, err = createIO(ctx, p.id, p.IoUID, p.IoGID, p.stdio); err != nil {
			return fmt.Errorf("failed to create init process I/O: %w", err)
		}
		p.io = pio
	}
	if r.Checkpoint != "" {
		return p.createCheckpointedState(r, pidFile)
	}
	opts := &runc.CreateOpts{
		PidFile:      pidFile.Path(),
		NoPivot:      p.NoPivotRoot,
		NoNewKeyring: p.NoNewKeyring,
	}
	if p.io != nil {
		opts.IO = p.io.IO()
	}
	if socket != nil {
		opts.ConsoleSocket = socket
	}

	// runc ignores silently features it doesn't know about, so for things that this is
	// problematic let's check if this runc version supports them.
	if err := p.validateRuncFeatures(ctx, r.Bundle); err != nil {
		return fmt.Errorf("failed to detect OCI runtime features: %w", err)
	}

	if err := p.runtime.Create(ctx, r.ID, r.Bundle, opts); err != nil {
		return p.runtimeError(err, "OCI runtime create failed")
	}
	if r.Stdin != "" {
		if err := p.openStdin(r.Stdin); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if socket != nil {
		console, err := socket.ReceiveMaster()
		if err != nil {
			return fmt.Errorf("failed to retrieve console master: %w", err)
		}
		console, err = p.Platform.CopyConsole(ctx, console, p.id, r.Stdin, r.Stdout, r.Stderr, &p.wg)
		if err != nil {
			return fmt.Errorf("failed to start console copy: %w", err)
		}
		p.console = console
	} else {
		if err := pio.Copy(ctx, &p.wg); err != nil {
			return fmt.Errorf("failed to start io pipe copy: %w", err)
		}
	}
	pid, err := pidFile.Read()
	if err != nil {
		return fmt.Errorf("failed to retrieve OCI runtime container pid: %w", err)
	}
	p.pid = pid
	return nil
}

func (p *Init) validateRuncFeatures(ctx context.Context, bundle string) error {
	// TODO: We should remove the logic from here and rebase on #8509.
	// This way we can avoid the call to readConfig() here and the call to p.runtime.Features()
	// in validateIDMapMounts().
	// But that PR is not yet merged nor it is clear if it will be refactored.
	// Do this contained hack for now.
	spec, err := readConfig(bundle)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	if err := p.validateIDMapMounts(ctx, spec); err != nil {
		return fmt.Errorf("OCI runtime doesn't support idmap mounts: %w", err)
	}

	return nil
}

func (p *Init) validateIDMapMounts(ctx context.Context, spec *specs.Spec) error {
	var used bool
	for _, m := range spec.Mounts {
		if m.UIDMappings != nil || m.GIDMappings != nil {
			used = true
			break
		}
		if sliceContainsStr(m.Options, "idmap") || sliceContainsStr(m.Options, "ridmap") {
			used = true
			break
		}
	}

	if !used {
		return nil
	}

	// From here onwards, we require idmap mounts. So if we fail to check, we return an error.
	features, err := p.runtime.Features(ctx)
	if err != nil {
		// If the features command is not implemented, then runc is too old.
		return fmt.Errorf("features command failed: %w", err)

	}

	if features.Linux.MountExtensions == nil || features.Linux.MountExtensions.IDMap == nil {
		return errors.New("missing `mountExtensions.idmap` entry in `features` command")
	}

	if enabled := features.Linux.MountExtensions.IDMap.Enabled; enabled == nil || !*enabled {
		return errors.New("idmap mounts not supported")
	}

	return nil
}

func (p *Init) openStdin(path string) error {
	sc, err := fifo.OpenFifo(context.Background(), path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("failed to open stdin fifo %s: %w", path, err)
	}
	p.stdin = sc
	p.closers = append(p.closers, sc)
	return nil
}

func (p *Init) createCheckpointedState(r *CreateConfig, pidFile *pidFile) error {
	opts := &runc.RestoreOpts{
		CheckpointOpts: runc.CheckpointOpts{
			ImagePath:  r.Checkpoint,
			WorkDir:    p.CriuWorkPath,
			ParentPath: r.ParentCheckpoint,
		},
		PidFile:     pidFile.Path(),
		NoPivot:     p.NoPivotRoot,
		Detach:      true,
		NoSubreaper: true,
	}

	if p.io != nil {
		opts.IO = p.io.IO()
	}

	p.initState = &createdCheckpointState{
		p:    p,
		opts: opts,
	}
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
	if p.pausing.Load() {
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
	if err2 := mount.UnmountRecursive(p.Rootfs, 0); err2 != nil {
		log.G(ctx).WithError(err2).Warn("failed to cleanup rootfs mount")
		if err == nil {
			err = fmt.Errorf("failed rootfs umount: %w", err2)
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

	err := p.runtime.Kill(ctx, p.id, int(unix.SIGKILL), &runc.KillOpts{
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
func (p *Init) Exec(ctx context.Context, path string, r *ExecConfig) (Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Exec(ctx, path, r)
}

// exec returns a new exec'd process
func (p *Init) exec(ctx context.Context, path string, r *ExecConfig) (Process, error) {
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
		stdio: stdio.Stdio{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
		waitBlock: make(chan struct{}),
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
	// keep criu work directory if criu work dir is set
	work := r.WorkDir
	if work == "" {
		work = filepath.Join(p.WorkDir, "criu-work")
		defer os.RemoveAll(work)
	}
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
			log.G(ctx).WithError(cerr).Error("failed to copy dump.log to criu-dump.log")
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
func (p *Init) Stdio() stdio.Stdio {
	return p.stdio
}

func (p *Init) runtimeError(rErr error, msg string) error {
	if rErr == nil {
		return nil
	}

	rMsg, err := getLastRuntimeError(p.runtime)
	switch {
	case err != nil:
		return fmt.Errorf("%s: %s (%s): %w", msg, "unable to retrieve OCI runtime error", err.Error(), rErr)
	case rMsg == "":
		return fmt.Errorf("%s: %w", msg, rErr)
	default:
		return fmt.Errorf("%s: %s", msg, rMsg)
	}
}

func withConditionalIO(c stdio.Stdio) runc.IOOpt {
	return func(o *runc.IOOption) {
		o.OpenStdin = c.Stdin != ""
		o.OpenStdout = c.Stdout != ""
		o.OpenStderr = c.Stderr != ""
	}
}

func sliceContainsStr(s []string, str string) bool {
	for _, s := range s {
		if s == str {
			return true
		}
	}
	return false
}
