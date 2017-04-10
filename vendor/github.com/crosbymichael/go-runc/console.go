// +build cgo

package runc

import (
	"fmt"
	"net"
	"path/filepath"

	"github.com/crosbymichael/console"
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

// Socket is a unix socket that accepts the pty master created by runc
type Socket struct {
	path string
	l    net.Listener
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
	return c.l.Close()
}
