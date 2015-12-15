package runtime

import (
	"io"
	"os"
	"time"

	"github.com/opencontainers/specs"
)

type Process interface {
	io.Closer
	Pid() (int, error)
	Spec() specs.Process
	Signal(os.Signal) error
	IO() *IO
}

type Status string

const (
	Paused  Status = "paused"
	Running Status = "running"
)

type State struct {
	Status Status
}

// Console represents a pseudo TTY.
type Console interface {
	io.ReadWriter
	io.Closer

	// Path returns the filesystem path to the slave side of the pty.
	Path() string

	// Fd returns the fd for the master of the pty.
	Fd() uintptr
}

type IO struct {

	// TODO(stevvooe): Consider matching StdinPipe, etc. methods from os/exec
	// and ssh.Session.

	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser

	console Console // present, if available
}

func (i *IO) Console() (Console, error) {
	if i.console == nil {
		return nil, ErrConsoleUnavailable
	}

	return i.console, nil
}

func (i *IO) Close() error {
	var oerr error
	for _, c := range []io.Closer{
		i.Stdin,
		i.Stdout,
		i.Stderr,
	} {
		if c != nil {
			if err := c.Close(); oerr == nil {
				oerr = err
			}
		}
	}
	return oerr
}

type Stat struct {
	// Timestamp is the time that the statistics where collected
	Timestamp time.Time
	// Data is the raw stats
	// TODO: it is currently an interface because we don't know what type of exec drivers
	// we will have or what the structure should look like at the moment os the containers
	// can return what they want and we could marshal to json or whatever.
	Data interface{}
}

type Checkpoint struct {
	// Timestamp is the time that checkpoint happened
	Timestamp time.Time
	// Name is the name of the checkpoint
	Name string
	// Tcp checkpoints open tcp connections
	Tcp bool
	// UnixSockets persists unix sockets in the checkpoint
	UnixSockets bool
	// Shell persists tty sessions in the checkpoint
	Shell bool
	// Exit exits the container after the checkpoint is finished
	Exit bool
}

type Container interface {
	// ID returns the container ID
	ID() string
	// Start starts the init process of the container
	Start() error
	// Path returns the path to the bundle
	Path() string
	// Pid returns the container's init process id
	Pid() (int, error)
	// SetExited sets the exit status of the container after it's init dies
	SetExited(status int)
	// Delete deletes the container
	Delete() error
	// Processes returns all the containers processes that have been added
	Processes() ([]Process, error)
	// RemoveProcess removes a specific process for the container because it exited
	RemoveProcess(pid int) error
	// State returns the containers runtime state
	State() State
	// Resume resumes a paused container
	Resume() error
	// Pause pauses a running container
	Pause() error
	// Checkpoints returns all the checkpoints for a container
	Checkpoints() ([]Checkpoint, error)
	// Checkpoint creates a new checkpoint
	Checkpoint(Checkpoint) error
	// DeleteCheckpoint deletes the checkpoint for the provided name
	DeleteCheckpoint(name string) error
	// Restore restores the container to that of the checkpoint provided by name
	Restore(name string) error
	// Stats returns realtime container stats and resource information
	Stats() (*Stat, error)
}
