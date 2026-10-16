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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/containerd/console"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"github.com/containerd/errdefs"
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
	"github.com/containerd/log"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type execProcess struct {
	wg sync.WaitGroup

	execState execState

	// mu is used to ensure that the exec process state machine is only
	// advanced by one operation at a time.
	mu sync.Mutex

	// statusMu guards the observable status — execState, status and exited —
	// separately from mu so that the read-only accessors can answer from it
	// while a Delete is in flight. Delete holds mu across slow work (the stdio
	// drain, the runc delete exec and closing the IO), which would otherwise
	// block the State RPC past its deadline. Lock order is mu -> statusMu,
	// never the reverse.
	statusMu sync.Mutex

	// deleting is set for the duration of a Delete. Only then may Status skip
	// mu: every other lifecycle operation still serializes readers through mu,
	// so State cannot bypass an in-flight Start or return before SetExited has
	// published the stopped state.
	deleting atomic.Bool

	id      string
	console console.Console
	io      *processIO
	status  int
	exited  time.Time
	pid     safePid
	closers []io.Closer
	stdin   io.Closer
	stdio   stdio.Stdio
	path    string
	spec    specs.Process

	parent    *Init
	waitBlock chan struct{}
}

func (e *execProcess) Wait(ctx context.Context) error {
	select {
	case <-e.waitBlock:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *execProcess) ID() string {
	return e.id
}

func (e *execProcess) Pid() int {
	return e.pid.get()
}

// exitedAt returns the time the process exited. Callers may hold mu;
// statusMu is taken internally so the field can be shared with the status
// accessors (lock order mu -> statusMu).
func (e *execProcess) exitedAt() time.Time {
	e.statusMu.Lock()
	defer e.statusMu.Unlock()
	return e.exited
}

// setExecState swaps the state machine. Callers must hold mu; statusMu is
// taken as well so the read-only Status accessor observes a consistent
// execState pointer (lock order mu -> statusMu).
func (e *execProcess) setExecState(state execState) {
	e.statusMu.Lock()
	e.execState = state
	e.statusMu.Unlock()
}

func (e *execProcess) ExitStatus() int {
	e.statusMu.Lock()
	defer e.statusMu.Unlock()
	return e.status
}

func (e *execProcess) ExitedAt() time.Time {
	e.statusMu.Lock()
	defer e.statusMu.Unlock()
	return e.exited
}

func (e *execProcess) SetExited(status int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.execState.SetExited(status)
}

func (e *execProcess) setExited(status int) {
	e.statusMu.Lock()
	e.status = status
	e.exited = time.Now()
	e.statusMu.Unlock()

	e.parent.Platform.ShutdownConsole(context.Background(), e.console)
	close(e.waitBlock)
}

func (e *execProcess) Delete(ctx context.Context) error {
	// Announce the delete before taking mu so the accessors can answer from the
	// observable state instead of waiting for the slow cleanup below.
	e.deleting.Store(true)
	defer e.deleting.Store(false)

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.execState.Delete(ctx)
}

func (e *execProcess) delete(ctx context.Context) error {
	if err := waitTimeout(ctx, &e.wg, 10*time.Second); err != nil {
		log.G(ctx).WithError(err).Errorf("failed to drain exec process %s io", e.id)
	}
	if e.io != nil {
		for _, c := range e.closers {
			c.Close()
		}
		e.io.Close()
	}
	pidfile := filepath.Join(e.path, e.id+".pid")
	// silently ignore error
	os.Remove(pidfile)
	return nil
}

func (e *execProcess) Resize(ws console.WinSize) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.execState.Resize(ws)
}

func (e *execProcess) resize(ws console.WinSize) error {
	if e.console == nil {
		return nil
	}
	return e.console.Resize(ws)
}

func (e *execProcess) Kill(ctx context.Context, sig uint32, _ bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.execState.Kill(ctx, sig, false)
}

func (e *execProcess) kill(ctx context.Context, sig uint32, _ bool) error {
	pid := e.pid.get()
	switch {
	case pid == 0:
		return fmt.Errorf("process not created: %w", errdefs.ErrFailedPrecondition)
	case !e.exitedAt().IsZero():
		return fmt.Errorf("process already finished: %w", errdefs.ErrNotFound)
	default:
		if err := unix.Kill(pid, syscall.Signal(sig)); err != nil {
			return fmt.Errorf("exec kill error: %w", checkKillError(err))
		}
	}
	return nil
}

func (e *execProcess) Stdin() io.Closer {
	return e.stdin
}

func (e *execProcess) Stdio() stdio.Stdio {
	return e.stdio
}

func (e *execProcess) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.execState.Start(ctx)
}

func (e *execProcess) start(ctx context.Context) (err error) {
	// The reaper may receive exit signal right after
	// the container is started, before the e.pid is updated.
	// In that case, we want to block the signal handler to
	// access e.pid until it is updated.
	e.pid.Lock()
	defer e.pid.Unlock()

	var (
		socket  *runc.Socket
		pio     *processIO
		pidFile = newExecPidFile(e.path, e.id)
	)
	if e.stdio.Terminal {
		if socket, err = runc.NewTempConsoleSocket(); err != nil {
			return fmt.Errorf("failed to create runc console socket: %w", err)
		}
		defer socket.Close()
	} else {
		if pio, err = createIO(ctx, e.id, e.parent.IoUID, e.parent.IoGID, e.stdio); err != nil {
			return fmt.Errorf("failed to create init process I/O: %w", err)
		}
		e.io = pio
	}
	opts := &runc.ExecOpts{
		PidFile: pidFile.Path(),
		Detach:  true,
	}
	if pio != nil {
		opts.IO = pio.IO()
	}
	if socket != nil {
		opts.ConsoleSocket = socket
	}
	if err := e.parent.runtime.Exec(ctx, e.parent.id, e.spec, opts); err != nil {
		close(e.waitBlock)
		return e.parent.runtimeError(err, "OCI runtime exec failed")
	}
	if e.stdio.Stdin != "" {
		if err := e.openStdin(e.stdio.Stdin); err != nil {
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
		if e.console, err = e.parent.Platform.CopyConsole(ctx, console, e.id, e.stdio.Stdin, e.stdio.Stdout, e.stdio.Stderr, &e.wg); err != nil {
			return fmt.Errorf("failed to start console copy: %w", err)
		}
	} else {
		if err := pio.Copy(ctx, &e.wg); err != nil {
			return fmt.Errorf("failed to start io pipe copy: %w", err)
		}
	}
	pid, err := pidFile.Read()
	if err != nil {
		return fmt.Errorf("failed to retrieve OCI runtime exec pid: %w", err)
	}
	e.pid.pid = pid
	return nil
}

func (e *execProcess) openStdin(path string) error {
	sc, err := fifo.OpenFifo(context.Background(), path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("failed to open stdin fifo %s: %w", path, err)
	}
	e.stdin = sc
	e.closers = append(e.closers, sc)
	return nil
}

func (e *execProcess) Status(ctx context.Context) (string, error) {
	s, err := e.parent.Status(ctx)
	if err != nil {
		return "", err
	}
	// if the container as a whole is in the pausing/paused state, so are all
	// other processes inside the container, use container state here
	switch s {
	case "paused", "pausing":
		return s, nil
	}

	if e.deleting.Load() {
		// A Delete is cleaning up while holding mu; answer from the observable
		// state rather than blocking the State RPC behind the slow work.
		e.statusMu.Lock()
		defer e.statusMu.Unlock()

		return e.execState.Status(ctx)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	return e.execState.Status(ctx)
}
