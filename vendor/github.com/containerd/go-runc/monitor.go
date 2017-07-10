package runc

import (
	"os/exec"
	"syscall"
)

var Monitor ProcessMonitor = &defaultMonitor{}

// ProcessMonitor is an interface for process monitoring
//
// It allows daemons using go-runc to have a SIGCHLD handler
// to handle exits without introducing races between the handler
// and go's exec.Cmd
// These methods should match the methods exposed by exec.Cmd to provide
// a consistent experience for the caller
type ProcessMonitor interface {
	Output(*exec.Cmd) ([]byte, error)
	CombinedOutput(*exec.Cmd) ([]byte, error)
	Run(*exec.Cmd) error
	Start(*exec.Cmd) error
	Wait(*exec.Cmd) (int, error)
}

type defaultMonitor struct {
}

func (m *defaultMonitor) Output(c *exec.Cmd) ([]byte, error) {
	return c.Output()
}

func (m *defaultMonitor) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	return c.CombinedOutput()
}

func (m *defaultMonitor) Run(c *exec.Cmd) error {
	return c.Run()
}

func (m *defaultMonitor) Start(c *exec.Cmd) error {
	return c.Start()
}

func (m *defaultMonitor) Wait(c *exec.Cmd) (int, error) {
	if err := c.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				return status.ExitStatus(), nil
			}
		}
		return -1, err
	}
	return 0, nil
}
