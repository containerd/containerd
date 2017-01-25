package shim

import runc "github.com/crosbymichael/go-runc"

type process interface {
	// Pid returns the pid for the process
	Pid() int
	// Resize resizes the process console
	Resize(ws runc.WinSize) error
	// Exited sets the exit status for the process
	Exited(status int)
	// Status returns the exit status
	Status() int
}
