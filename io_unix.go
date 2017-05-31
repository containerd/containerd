// +build !windows

package containerd

import (
	"context"
	"io"
	"sync"
	"syscall"

	"github.com/containerd/fifo"
)

func copyIO(fifos *FifoSet, ioset *ioSet, tty bool) (closer io.Closer, err error) {
	var (
		f   io.ReadWriteCloser
		ctx = context.Background()
		wg  = &sync.WaitGroup{}
	)

	if f, err = fifo.OpenFifo(ctx, fifos.In, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	go func(w io.WriteCloser) {
		io.Copy(w, ioset.in)
		w.Close()
	}(f)

	if f, err = fifo.OpenFifo(ctx, fifos.Out, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	wg.Add(1)
	go func(r io.ReadCloser) {
		io.Copy(ioset.out, r)
		r.Close()
		wg.Done()
	}(f)

	if f, err = fifo.OpenFifo(ctx, fifos.Err, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)

	if !tty {
		wg.Add(1)
		go func(r io.ReadCloser) {
			io.Copy(ioset.err, r)
			r.Close()
			wg.Done()
		}(f)
	}

	return &wgCloser{
		wg:  wg,
		dir: fifos.Dir,
	}, nil
}
