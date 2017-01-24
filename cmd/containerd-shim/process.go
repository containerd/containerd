package main

import (
	"context"
	"errors"

	runc "github.com/crosbymichael/go-runc"
)

var errRuntime = errors.New("shim: runtime execution error")

type process interface {
	// Pid returns the pid for the process
	Pid() int
	// Start starts the user's defined process inside
	Start(context.Context) error
	// Delete deletes the process and closes all open pipes
	Delete(context.Context) error
	// Resize resizes the process console
	Resize(ws runc.WinSize) error
	// Exited sets the exit status for the process
	Exited(status int)
	// Status returns the exit status
	Status() int
}
