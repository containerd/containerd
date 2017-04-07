// +build windows

package hcs

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/containerd/containerd"
	"github.com/pkg/errors"
)

type shimIO struct {
	stdin    net.Conn
	stdout   net.Conn
	stderr   net.Conn
	terminal bool
}

// newSIO connects to the provided pipes
func newSIO(io containerd.IO) (*shimIO, error) {
	var (
		c   net.Conn
		err error
		sio shimIO
	)

	defer func() {
		if err != nil {
			sio.Close()
		}
	}()

	for _, p := range []struct {
		name string
		open bool
		conn *net.Conn
	}{
		{
			name: io.Stdin,
			open: io.Stdin != "",
			conn: &sio.stdin,
		},
		{
			name: io.Stdout,
			open: io.Stdout != "",
			conn: &sio.stdout,
		},
		{
			name: io.Stderr,
			open: !io.Terminal && io.Stderr != "",
			conn: &sio.stderr,
		},
	} {
		if p.open {
			dialTimeout := 3 * time.Second
			c, err = winio.DialPipe(p.name, &dialTimeout)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to connect to %s", p.name)
			}
			*p.conn = c
		}
	}

	return &sio, nil
}

// Close terminates all successfully dialed IO connections
func (s *shimIO) Close() {
	for _, cn := range []net.Conn{s.stdin, s.stdout, s.stderr} {
		if cn != nil {
			cn.Close()
			cn = nil
		}
	}
}
