package runc

import (
	"fmt"
	"net"
	"os"

	"github.com/docker/docker/pkg/term"
	"github.com/opencontainers/runc/libcontainer/utils"
)

// NewConsoleSocket creates a new unix socket at the provided path to accept a
// pty master created by runc for use by the container
func NewConsoleSocket(path string) (*ConsoleSocket, error) {
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return &ConsoleSocket{
		l:    l,
		path: path,
	}, nil
}

// ConsoleSocket is a unix socket that accepts the pty master created by runc
type ConsoleSocket struct {
	path string
	l    net.Listener
}

// Path returns the path to the unix socket on disk
func (c *ConsoleSocket) Path() string {
	return c.path
}

// ReceiveMaster blocks until the socket receives the pty master
func (c *ConsoleSocket) ReceiveMaster() (*Console, error) {
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
	return &Console{
		master: f,
	}, nil
}

// Close closes the unix socket
func (c *ConsoleSocket) Close() error {
	return c.l.Close()
}

// WinSize specifies the console size
type WinSize struct {
	// Width of the console
	Width uint16
	// Height of the console
	Height uint16
}

// Console is a pty master
type Console struct {
	master *os.File
}

// Read from the console
func (c *Console) Read(b []byte) (int, error) {
	return c.master.Read(b)
}

// Write writes to the console
func (c *Console) Write(b []byte) (int, error) {
	return c.master.Write(b)
}

// Resize the console
func (c *Console) Resize(ws WinSize) error {
	return term.SetWinsize(c.master.Fd(), &term.Winsize{
		Width:  ws.Width,
		Height: ws.Height,
	})
}

// Close the console
func (c *Console) Close() error {
	return c.master.Close()
}
