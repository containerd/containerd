// +build !windows

package shim

import (
	"context"
	"io"
	"time"

	"github.com/containerd/console"
)

type stdio struct {
	stdin    string
	stdout   string
	stderr   string
	terminal bool
}

type process interface {
	// ID returns the id for the process
	ID() string
	// Pid returns the pid for the process
	Pid() int
	// Resize resizes the process console
	Resize(ws console.WinSize) error
	// Exited sets the exit status for the process
	Exited(status int)
	// Status returns the exit status
	Status() int
	// ExitedAt is the time the process exited
	ExitedAt() time.Time
	// Delete deletes the process and its resourcess
	Delete(context.Context) error
	// Stdin returns the process STDIN
	Stdin() io.Closer
	// Kill kills the process
	Kill(context.Context, uint32, bool) error
	// Stdio returns io information for the container
	Stdio() stdio
}
