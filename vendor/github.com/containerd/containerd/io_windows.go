package containerd

import (
	"io"
	"net"
	"sync"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

func copyIO(fifos *FifoSet, ioset *ioSet, tty bool) (closer io.Closer, err error) {
	var wg sync.WaitGroup

	if fifos.In != "" {
		l, err := winio.ListenPipe(fifos.In, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stdin pipe %s", fifos.In)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)

		go func() {
			c, err := l.Accept()
			if err != nil {
				log.L.WithError(err).Errorf("failed to accept stdin connection on %s", fifos.In)
				return
			}
			io.Copy(c, ioset.in)
			c.Close()
			l.Close()
		}()
	}

	if fifos.Out != "" {
		l, err := winio.ListenPipe(fifos.Out, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stdin pipe %s", fifos.Out)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)

		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := l.Accept()
			if err != nil {
				log.L.WithError(err).Errorf("failed to accept stdout connection on %s", fifos.Out)
				return
			}
			io.Copy(ioset.out, c)
			c.Close()
			l.Close()
		}()
	}

	if !tty && fifos.Err != "" {
		l, err := winio.ListenPipe(fifos.Err, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stderr pipe %s", fifos.Err)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)

		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := l.Accept()
			if err != nil {
				log.L.WithError(err).Errorf("failed to accept stderr connection on %s", fifos.Err)
				return
			}
			io.Copy(ioset.err, c)
			c.Close()
			l.Close()
		}()
	}

	return &wgCloser{
		wg:  &wg,
		dir: fifos.Dir,
	}, nil
}
