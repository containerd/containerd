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
)

// newFIFOSetTempDir returns a new set of fifos for the task
func newFIFOSetTempDir(id string) (*FIFOSet, error) {
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

func copyIO(fifos *FIFOSet, ioset *ioSet, tty bool) (_ *wgCloser, err error) {
	var (
		f           io.ReadWriteCloser
		ctx, cancel = context.WithCancel(context.Background())
		wg          = &sync.WaitGroup{}
	)
	set := []io.Closer{fifos}
	defer func() {
		if err != nil {
			for _, f := range set {
				f.Close()
			}
			cancel()
		}
	}()

	if f, err = fifo.OpenFifo(ctx, fifos.Stdin, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	set = append(set, f)
	go func(w io.WriteCloser) {
		io.Copy(w, ioset.in)
		w.Close()
	}(f)

	if f, err = fifo.OpenFifo(ctx, fifos.Stdout, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	set = append(set, f)
	wg.Add(1)
	go func(r io.ReadCloser) {
		io.Copy(ioset.out, r)
		r.Close()
		wg.Done()
	}(f)

	if f, err = fifo.OpenFifo(ctx, fifos.Stderr, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	set = append(set, f)

	if !tty {
		wg.Add(1)
		go func(r io.ReadCloser) {
			io.Copy(ioset.err, r)
			r.Close()
			wg.Done()
		}(f)
	}
	return &wgCloser{
		wg:     wg,
		set:    set,
		cancel: cancel,
	}, nil
}

// NewDirectIO returns an IO implementation that exposes the pipes directly
func NewDirectIO(ctx context.Context, terminal bool) (*DirectIO, error) {
	set, err := newFIFOSetTempDir("")
	if err != nil {
		return nil, err
	}
	set.Terminal = terminal
	f := &DirectIO{set: set}

	defer func() {
		if err != nil {
			f.Delete()
		}
	}()
	if f.Stdin, err = fifo.OpenFifo(ctx, set.Stdin, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	if f.Stdout, err = fifo.OpenFifo(ctx, set.Stdout, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		f.Stdin.Close()
		return nil, err
	}
	if f.Stderr, err = fifo.OpenFifo(ctx, set.Stderr, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		f.Stdin.Close()
		f.Stdout.Close()
		return nil, err
	}
	return f, nil
}

// DirectIO allows task IO to be handled externally by the caller
type DirectIO struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser

	set *FIFOSet
}

// IOCreate returns IO avaliable for use with task creation
func (f *DirectIO) IOCreate(id string) (IO, error) {
	return f, nil
}

// IOAttach returns IO avaliable for use with task attachment
func (f *DirectIO) IOAttach(set *FIFOSet) (IO, error) {
	return f, nil
}

// Config returns the Config
func (f *DirectIO) Config() Config {
	return f.set.Config
}

// Cancel stops any IO copy operations
//
// Not applicable for DirectIO
func (f *DirectIO) Cancel() {
	// nothing to cancel as all operations are handled externally
}

// Wait on any IO copy operations
//
// Not applicable for DirectIO
func (f *DirectIO) Wait() {
	// nothing to wait on as all operations are handled externally
}

// Close closes all open fds
func (f *DirectIO) Close() error {
	err := f.Stdin.Close()
	if err2 := f.Stdout.Close(); err == nil {
		err = err2
	}
	if err2 := f.Stderr.Close(); err == nil {
		err = err2
	}
	return err
}

// Delete removes the underlying directory containing fifos
func (f *DirectIO) Delete() error {
	return f.set.Close()
}
