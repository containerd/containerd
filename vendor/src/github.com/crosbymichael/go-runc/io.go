package runc

import (
	"io"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

type IO interface {
	io.Closer
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Set(*exec.Cmd)
}

type StartCloser interface {
	CloseAfterStart() error
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
	if err := unix.Fchown(int(r.Fd()), uid, gid); err != nil {
		return nil, err
	}
	if err := unix.Fchown(int(w.Fd()), uid, gid); err != nil {
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
	return i.out.r
}

func (i *pipeIO) Stderr() io.ReadCloser {
	return i.err.r
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

func (i *pipeIO) CloseAfterStart() error {
	for _, f := range []*os.File{
		i.out.w,
		i.err.w,
	} {
		f.Close()
	}
	return nil
}

// Set sets the io to the exec.Cmd
func (i *pipeIO) Set(cmd *exec.Cmd) {
	cmd.Stdin = i.in.r
	cmd.Stdout = i.out.w
	cmd.Stderr = i.err.w
}

func NewSTDIO() (IO, error) {
	return &stdio{}, nil
}

type stdio struct {
}

func (s *stdio) Close() error {
	return nil
}

func (s *stdio) Set(cmd *exec.Cmd) {
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
}

func (s *stdio) Stdin() io.WriteCloser {
	return os.Stdin
}

func (s *stdio) Stdout() io.ReadCloser {
	return os.Stdout
}

func (s *stdio) Stderr() io.ReadCloser {
	return os.Stderr
}
