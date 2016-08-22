package exec

import (
	"fmt"
	osExec "os/exec"
	"strconv"

	"github.com/docker/containerd/subreaper"
)

var ErrNotFound = osExec.ErrNotFound

type Cmd struct {
	osExec.Cmd
	err error
	sub *subreaper.Subscription
}

type Error struct {
	Name string
	Err  error
}

func (e *Error) Error() string {
	return "exec: " + strconv.Quote(e.Name) + ": " + e.Err.Error()
}

type ExitCodeError struct {
	Code int
}

func (e ExitCodeError) Error() string {
	return fmt.Sprintf("Non-zero exit code: %d", e.Code)
}

func LookPath(file string) (string, error) {
	v, err := osExec.LookPath(file)
	return v, translateError(err)
}

func Command(name string, args ...string) *Cmd {
	return &Cmd{
		Cmd: *osExec.Command(name, args...),
	}
}

func (c *Cmd) Start() error {
	c.sub = subreaper.Subscribe()
	err := c.Cmd.Start()
	if err != nil {
		subreaper.Unsubscribe(c.sub)
		c.sub = nil
		c.err = translateError(err)
		return c.err
	}

	c.sub.SetPid(c.Cmd.Process.Pid)
	return nil
}

func (c *Cmd) Wait() error {
	// c.Cmd.Wait() will always error because there is no child process anymore
	// This is called to ensure that the streams are closed and cleaned up properly
	defer c.Cmd.Wait()
	if c.sub == nil {
		return c.err
	}
	exitCode := c.sub.Wait()
	if exitCode == 0 {
		return nil
	}
	return ExitCodeError{Code: exitCode}
}

func translateError(err error) error {
	switch v := err.(type) {
	case *osExec.Error:
		return &Error{
			Name: v.Name,
			Err:  v.Err,
		}
	}
	return err
}
