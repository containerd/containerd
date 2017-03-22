package shim

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/crosbymichael/console"
	runc "github.com/crosbymichael/go-runc"
	shimapi "github.com/docker/containerd/api/services/shim"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type execProcess struct {
	sync.WaitGroup

	id      int
	console console.Console
	io      runc.IO
	status  int
	pid     int

	parent *initProcess
}

func newExecProcess(context context.Context, path string, r *shimapi.ExecRequest, parent *initProcess, id int) (process, error) {
	e := &execProcess{
		id:     id,
		parent: parent,
	}
	var (
		err     error
		socket  *runc.ConsoleSocket
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
		PidFile:       pidfile,
		ConsoleSocket: socket,
		IO:            io,
		Detach:        true,
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
	if socket != nil {
		console, err := socket.ReceiveMaster()
		if err != nil {
			return nil, err
		}
		e.console = console
		if err := copyConsole(context, console, r.Stdin, r.Stdout, r.Stderr, &e.WaitGroup); err != nil {
			return nil, err
		}
	} else {
		if err := copyPipes(context, io, r.Stdin, r.Stdout, r.Stderr, &e.WaitGroup); err != nil {
			return nil, err
		}
	}
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

func (e *execProcess) Exited(status int) {
	e.status = status
	e.Wait()
	if e.io != nil {
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
