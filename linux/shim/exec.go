// +build !windows

package shim

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/containerd/console"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

type execProcess struct {
	sync.WaitGroup

	id      int
	console console.Console
	io      runc.IO
	status  int
	exited  time.Time
	pid     int
	closers []io.Closer
	stdin   io.Closer

	parent *initProcess

	stdinPath  string
	stdoutPath string
	stderrPath string
	terminal   bool
}

func newExecProcess(context context.Context, path string, r *shimapi.ExecProcessRequest, parent *initProcess, id int) (process, error) {
	e := &execProcess{
		id:         id,
		parent:     parent,
		stdinPath:  r.Stdin,
		stdoutPath: r.Stdout,
		stderrPath: r.Stderr,
		terminal:   r.Terminal,
	}
	var (
		err     error
		socket  *runc.Socket
		io      runc.IO
		pidfile = filepath.Join(path, fmt.Sprintf("%d.pid", id))
	)
	if r.Terminal {
		if socket, err = runc.NewConsoleSocket(filepath.Join(path, "pty.sock")); err != nil {
			return nil, errors.Wrap(err, "failed to create runc console socket")
		}
		defer os.Remove(socket.Path())
	} else {
		// TODO: get uid/gid
		if io, err = runc.NewPipeIO(0, 0); err != nil {
			return nil, errors.Wrap(err, "failed to create runc io pipes")
		}
		e.io = io
	}
	opts := &runc.ExecOpts{
		PidFile: pidfile,
		IO:      io,
		Detach:  true,
	}
	if socket != nil {
		opts.ConsoleSocket = socket
	}
	// process exec request
	var spec specs.Process
	if err := json.Unmarshal(r.Spec.Value, &spec); err != nil {
		return nil, err
	}
	spec.Terminal = r.Terminal

	if err := parent.runtime.Exec(context, parent.id, spec, opts); err != nil {
		return nil, parent.runtimeError(err, "OCI runtime exec failed")
	}
	if r.Stdin != "" {
		sc, err := fifo.OpenFifo(context, r.Stdin, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to open stdin fifo %s", r.Stdin)
		}
		e.closers = append(e.closers, sc)
		e.stdin = sc
	}
	var copyWaitGroup sync.WaitGroup
	if socket != nil {
		console, err := socket.ReceiveMaster()
		if err != nil {
			return nil, errors.Wrap(err, "failed to retrieve console master")
		}
		e.console = console
		if err := copyConsole(context, console, r.Stdin, r.Stdout, r.Stderr, &e.WaitGroup, &copyWaitGroup); err != nil {
			return nil, errors.Wrap(err, "failed to start console copy")
		}
	} else {
		if err := copyPipes(context, io, r.Stdin, r.Stdout, r.Stderr, &e.WaitGroup, &copyWaitGroup); err != nil {
			return nil, errors.Wrap(err, "failed to start io pipe copy")
		}
	}
	copyWaitGroup.Wait()
	pid, err := runc.ReadPidFile(opts.PidFile)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve OCI runtime exec pid")
	}
	e.pid = pid
	return e, nil
}

func (e *execProcess) Pid() int {
	return e.pid
}

func (e *execProcess) Status() int {
	return e.status
}

func (e *execProcess) ExitedAt() time.Time {
	return e.exited
}

func (e *execProcess) Exited(status int) {
	e.status = status
	e.exited = time.Now()
	e.Wait()
	if e.io != nil {
		for _, c := range e.closers {
			c.Close()
		}
		e.io.Close()
	}
}

func (e *execProcess) Delete(ctx context.Context) error {
	return nil
}

func (e *execProcess) Resize(ws console.WinSize) error {
	if e.console == nil {
		return nil
	}
	return e.console.Resize(ws)
}

func (e *execProcess) Signal(sig int) error {
	if err := unix.Kill(e.pid, syscall.Signal(sig)); err != nil {
		return checkKillError(err)
	}
	return nil
}

func (e *execProcess) Stdin() io.Closer {
	return e.stdin
}
