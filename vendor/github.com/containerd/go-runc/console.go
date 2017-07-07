// +build cgo

package runc

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"

	"github.com/containerd/console"
	"github.com/opencontainers/runc/libcontainer/utils"
)

// NewConsoleSocket creates a new unix socket at the provided path to accept a
// pty master created by runc for use by the container
func NewConsoleSocket(path string) (*Socket, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", abs)
	if err != nil {
		return nil, err
	}
	return &Socket{
		l:    l,
		path: abs,
	}, nil
}

// NewTempConsoleSocket returns a temp console socket for use with a container
// On Close(), the socket is deleted
func NewTempConsoleSocket() (*Socket, error) {
	dir, err := ioutil.TempDir("", "pty")
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(filepath.Join(dir, "pty.sock"))
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", abs)
	if err != nil {
		return nil, err
	}
	return &Socket{
		l:     l,
		rmdir: true,
		path:  abs,
	}, nil
}

// Socket is a unix socket that accepts the pty master created by runc
type Socket struct {
	path  string
	rmdir bool
	l     net.Listener
}

// Path returns the path to the unix socket on disk
func (c *Socket) Path() string {
	return c.path
}

// ReceiveMaster blocks until the socket receives the pty master
func (c *Socket) ReceiveMaster() (console.Console, error) {
	conn, err := c.l.Accept()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("received connection which was not a unix socket")
	}
	sock, err := unix.File()
	if err != nil {
		return nil, err
	}
	f, err := utils.RecvFd(sock)
	if err != nil {
		return nil, err
	}
	return console.ConsoleFromFile(f)
}

// Close closes the unix socket
func (c *Socket) Close() error {
	err := c.l.Close()
	if c.rmdir {
		if rerr := os.RemoveAll(filepath.Dir(c.path)); err == nil {
			err = rerr
		}
	}
	return err
}
