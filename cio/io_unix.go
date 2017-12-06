// +build !windows

package cio

import (
	"context"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/containerd/fifo"
	"github.com/pkg/errors"
)

// newFIFOSetInTempDir returns a new set of fifos for the task
func newFIFOSetInTempDir(id string) (*FIFOSet, error) {
	root := "/run/containerd/fifo"
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	dir, err := ioutil.TempDir(root, "")
	if err != nil {
		return nil, err
	}
	closer := func() error {
		return os.RemoveAll(dir)
	}
	return NewFIFOSet(Config{
		Stdin:  filepath.Join(dir, id+"-stdin"),
		Stdout: filepath.Join(dir, id+"-stdout"),
		Stderr: filepath.Join(dir, id+"-stderr"),
	}, closer), nil
}

func copyIO(fifos *FIFOSet, ioset *ioSet) (*cio, error) {
	var (
		ctx, cancel = context.WithCancel(context.Background())
		wg          = &sync.WaitGroup{}
	)

	pipes, err := openFifos(ctx, fifos)
	if err != nil {
		cancel()
		return nil, err
	}

	if fifos.Stdin != "" {
		go func() {
			io.Copy(pipes.Stdin, ioset.in)
			pipes.Stdin.Close()
		}()
	}

	wg.Add(1)
	go func() {
		io.Copy(ioset.out, pipes.Stdout)
		pipes.Stdout.Close()
		wg.Done()
	}()

	if !fifos.Terminal {
		wg.Add(1)
		go func() {
			io.Copy(ioset.err, pipes.Stderr)
			pipes.Stderr.Close()
			wg.Done()
		}()
	}
	return &cio{
		wg:      wg,
		closers: append(pipes.closers(), fifos),
		cancel:  cancel,
	}, nil
}

func openFifos(ctx context.Context, fifos *FIFOSet) (pipes, error) {
	var err error
	f := new(pipes)

	defer func() {
		if err != nil {
			fifos.Close()
		}
	}()

	if fifos.Stdin != "" {
		if f.Stdin, err = fifo.OpenFifo(ctx, fifos.Stdin, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
			return pipes{}, errors.Wrapf(err, "failed to open stdin fifo")
		}
	}
	if f.Stdout, err = fifo.OpenFifo(ctx, fifos.Stdout, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		f.Stdin.Close()
		return pipes{}, errors.Wrapf(err, "failed to open stdout fifo")
	}
	if f.Stderr, err = fifo.OpenFifo(ctx, fifos.Stderr, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		f.Stdin.Close()
		f.Stdout.Close()
		return pipes{}, errors.Wrapf(err, "failed to open stderr fifo")
	}
	return pipes{}, nil
}

// NewDirectIO returns an IO implementation that exposes the IO streams as io.ReadCloser
// and io.WriteCloser. FIFOs are created in /run/containerd/fifo.
func NewDirectIO(ctx context.Context, terminal bool) (*DirectIO, error) {
	fifos, err := newFIFOSetInTempDir("")
	if err != nil {
		return nil, err
	}
	fifos.Terminal = terminal

	ctx, cancel := context.WithCancel(context.Background())
	pipes, err := openFifos(ctx, fifos)
	return &DirectIO{
		pipes: pipes,
		cio: cio{
			config:  fifos.Config,
			closers: append(pipes.closers(), fifos),
			cancel:  cancel,
		},
	}, err
}

// DirectIO allows task IO to be handled externally by the caller
type DirectIO struct {
	pipes
	cio
}

var _ IO = &DirectIO{}
