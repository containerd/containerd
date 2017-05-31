package containerd

import (
	"io"
	"net"
	"sync"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

func copyIO(fifos *fifoSet, ioset *ioSet, tty bool) (closer io.Closer, err error) {
	var wg sync.WaitGroup

	if fifos.in != "" {
		l, err := winio.ListenPipe(fifos.in, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stdin pipe %s", fifos.in)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)

		go func() {
			c, err := l.Accept()
			if err != nil {
				log.L.WithError(err).Errorf("failed to accept stdin connection on %s", fifos.in)
				return
			}
			io.Copy(c, ioset.in)
			c.Close()
			l.Close()
		}()
	}

	if fifos.out != "" {
		l, err := winio.ListenPipe(fifos.out, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stdin pipe %s", fifos.out)
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
				log.L.WithError(err).Errorf("failed to accept stdout connection on %s", fifos.out)
				return
			}
			io.Copy(ioset.out, c)
			c.Close()
			l.Close()
		}()
	}

	if !tty && fifos.err != "" {
		l, err := winio.ListenPipe(fifos.err, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stderr pipe %s", fifos.err)
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
				log.L.WithError(err).Errorf("failed to accept stderr connection on %s", fifos.err)
				return
			}
			io.Copy(ioset.err, c)
			c.Close()
			l.Close()
		}()
	}

	return &wgCloser{
		wg:  &wg,
		dir: fifos.dir,
	}, nil
}
