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
	shimapi "github.com/containerd/containerd/api/services/shim"
	"github.com/containerd/fifo"
	runc "github.com/containerd/go-runc"
	specs "github.com/opencontainers/runtime-spec/specs-go"
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
}

func newExecProcess(context context.Context, path string, r *shimapi.ExecRequest, parent *initProcess, id int) (process, error) {
	e := &execProcess{
		id:     id,
		parent: parent,
	}
	var (
		err     error
		socket  *runc.Socket
		io      runc.IO
		pidfile = filepath.Join(path, fmt.Sprintf("%d.pid", id))
	)
	if r.Terminal {
		if socket, err = runc.NewConsoleSocket(filepath.Join(path, "pty.sock")); err != nil {
			return nil, err
		}
		defer os.Remove(socket.Path())
	} else {
		// TODO: get uid/gid
		if io, err = runc.NewPipeIO(0, 0); err != nil {
			return nil, err
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

	if err := parent.runc.Exec(context, parent.id, spec, opts); err != nil {
		return nil, err
	}
	if r.Stdin != "" {
		sc, err := fifo.OpenFifo(context, r.Stdin, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		e.closers = append(e.closers, sc)
		e.stdin = sc
	}
	var copyWaitGroup sync.WaitGroup
	if socket != nil {
		console, err := socket.ReceiveMaster()
		if err != nil {
			return nil, err
		}
		e.console = console
		if err := copyConsole(context, console, r.Stdin, r.Stdout, r.Stderr, &e.WaitGroup, &copyWaitGroup); err != nil {
			return nil, err
		}
	} else {
		if err := copyPipes(context, io, r.Stdin, r.Stdout, r.Stderr, &e.WaitGroup, &copyWaitGroup); err != nil {
			return nil, err
		}
	}
	copyWaitGroup.Wait()
	pid, err := runc.ReadPidFile(opts.PidFile)
	if err != nil {
		return nil, err
	}
	e.pid = pid
	return e, nil
}

func rlimits(rr []*shimapi.Rlimit) (o []specs.LinuxRlimit) {
	for _, r := range rr {
		o = append(o, specs.LinuxRlimit{
			Type: r.Type,
			Hard: r.Hard,
			Soft: r.Soft,
		})
	}
	return o
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
	return unix.Kill(e.pid, syscall.Signal(sig))
}

func (e *execProcess) Stdin() io.Closer {
	return e.stdin
}
