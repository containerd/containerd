// +build windows

package hcs

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/pkg/errors"
)

type IO struct {
	stdin    net.Conn
	stdout   net.Conn
	stderr   net.Conn
	terminal bool
}

// NewIO connects to the provided pipe addresses
func NewIO(stdin, stdout, stderr string, terminal bool) (*IO, error) {
	var (
		c   net.Conn
		err error
		io  IO
	)

	defer func() {
		if err != nil {
			io.Close()
		}
	}()

	for _, p := range []struct {
		name string
		open bool
		conn *net.Conn
	}{
		{
			name: stdin,
			open: stdin != "",
			conn: &io.stdin,
		},
		{
			name: stdout,
			open: stdout != "",
			conn: &io.stdout,
		},
		{
			name: stderr,
			open: !terminal && stderr != "",
			conn: &io.stderr,
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

	return &io, nil
}

// Close terminates all successfully dialed IO connections
func (i *IO) Close() {
	for _, cn := range []net.Conn{i.stdin, i.stdout, i.stderr} {
		if cn != nil {
			cn.Close()
			cn = nil
		}
	}
}
