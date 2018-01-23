// +build !windows

package shim

import (
	gocontext "context"
	"io"
	"os"
	"sync"

	"github.com/containerd/fifo"
	"golang.org/x/sys/unix"
)

var bufPool = sync.Pool{
	New: func() interface{} {
		buffer := make([]byte, 32<<10)
		return &buffer
	},
}

func prepareStdio(stdin, stdout, stderr string, console bool) (wg *sync.WaitGroup, err error) {
	wg = &sync.WaitGroup{}
	ctx := gocontext.Background()

	f, err := fifo.OpenFifo(ctx, stdin, unix.O_WRONLY|unix.O_CREAT|unix.O_NONBLOCK, 0700)
	if err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	go func(w io.WriteCloser) {
		p := bufPool.Get().(*[]byte)
		defer bufPool.Put(p)
		io.CopyBuffer(w, os.Stdin, *p)
		w.Close()
	}(f)

	f, err = fifo.OpenFifo(ctx, stdout, unix.O_RDONLY|unix.O_CREAT|unix.O_NONBLOCK, 0700)
	if err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	wg.Add(1)
	go func(r io.ReadCloser) {
		io.Copy(os.Stdout, r)
		r.Close()
		wg.Done()
	}(f)

	f, err = fifo.OpenFifo(ctx, stderr, unix.O_RDONLY|unix.O_CREAT|unix.O_NONBLOCK, 0700)
	if err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	if !console {
		wg.Add(1)
		go func(r io.ReadCloser) {
			io.Copy(os.Stderr, r)
			r.Close()
			wg.Done()
		}(f)
	}

	return wg, nil
}
