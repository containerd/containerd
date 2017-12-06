package cio

import (
	"fmt"
	"io"
	"net"
	"sync"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

const pipeRoot = `\\.\pipe`

// newFIFOSetInTempDir returns a new set of fifos for the task
func newFIFOSetInTempDir(id string) (*FIFOSet, error) {
	return &FIFOSet{
		StdIn:  fmt.Sprintf(`%s\ctr-%s-stdin`, pipeRoot, id),
		StdOut: fmt.Sprintf(`%s\ctr-%s-stdout`, pipeRoot, id),
		StdErr: fmt.Sprintf(`%s\ctr-%s-stderr`, pipeRoot, id),
	}, nil
}

func copyIO(fifos *FIFOSet, ioset *ioSet) (*cio, error) {
	var (
		err error
		wg  sync.WaitGroup
		set []io.Closer
	)

	if fifos.StdIn != "" {
		l, err := winio.ListenPipe(fifos.StdIn, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stdin pipe %s", fifos.StdIn)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)
		set = append(set, l)

		go func() {
			c, err := l.Accept()
			if err != nil {
				log.L.WithError(err).Errorf("failed to accept stdin connection on %s", fifos.StdIn)
				return
			}
			io.Copy(c, ioset.in)
			c.Close()
			l.Close()
		}()
	}

	if fifos.StdOut != "" {
		l, err := winio.ListenPipe(fifos.StdOut, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stdin pipe %s", fifos.StdOut)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)
		set = append(set, l)

		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := l.Accept()
			if err != nil {
				log.L.WithError(err).Errorf("failed to accept stdout connection on %s", fifos.StdOut)
				return
			}
			io.Copy(ioset.out, c)
			c.Close()
			l.Close()
		}()
	}

	if !fifos.Terminal && fifos.StdErr != "" {
		l, err := winio.ListenPipe(fifos.StdErr, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stderr pipe %s", fifos.StdErr)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)
		set = append(set, l)

		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := l.Accept()
			if err != nil {
				log.L.WithError(err).Errorf("failed to accept stderr connection on %s", fifos.StdErr)
				return
			}
			io.Copy(ioset.err, c)
			c.Close()
			l.Close()
		}()
	}

	return &cio{
		wg:  &wg,
		dir: fifos.Dir,
		set: set,
		cancel: func() {
			for _, l := range set {
				l.Close()
			}
		},
	}, nil
}
