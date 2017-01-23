package runc

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

type IO interface {
	io.Closer
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Set(*exec.Cmd)
}

// NewPipeIO creates pipe pairs to be used with runc
func NewPipeIO(uid, gid int) (i IO, err error) {
	var pipes []*pipe
	// cleanup in case of an error
	defer func() {
		if err != nil {
			for _, p := range pipes {
				p.Close()
			}
		}
	}()
	stdin, err := newPipe(uid, gid)
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stdin)

	stdout, err := newPipe(uid, gid)
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stdout)

	stderr, err := newPipe(uid, gid)
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stderr)

	return &pipeIO{
		in:  stdin,
		out: stdout,
		err: stderr,
	}, nil
}

func newPipe(uid, gid int) (*pipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	if err := syscall.Fchown(int(r.Fd()), uid, gid); err != nil {
		return nil, err
	}
	if err := syscall.Fchown(int(w.Fd()), uid, gid); err != nil {
		return nil, err
	}
	return &pipe{
		r: r,
		w: w,
	}, nil
}

type pipe struct {
	r *os.File
	w *os.File
}

func (p *pipe) Close() error {
	err := p.r.Close()
	if werr := p.w.Close(); err == nil {
		err = werr
	}
	return err
}

type pipeIO struct {
	in  *pipe
	out *pipe
	err *pipe
}

func (i *pipeIO) Stdin() io.WriteCloser {
	return i.in.w
}

func (i *pipeIO) Stdout() io.ReadCloser {
	return i.in.r
}

func (i *pipeIO) Stderr() io.ReadCloser {
	return i.in.r
}

func (i *pipeIO) Close() error {
	var err error
	for _, v := range []*pipe{
		i.in,
		i.out,
		i.err,
	} {
		if cerr := v.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// Set sets the io to the exec.Cmd
func (i *pipeIO) Set(cmd *exec.Cmd) {
	cmd.Stdin = i.in.r
	cmd.Stdout = i.out.w
	cmd.Stderr = i.err.w
}
