package reaper

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"

	"github.com/docker/containerd/utils"
)

// Reap should be called when the process receives an SIGCHLD.  Reap will reap
// all exited processes and close their wait channels
func Reap() error {
	exits, err := utils.Reap(false)
	for _, e := range exits {
		Default.Lock()
		c, ok := Default.cmds[e.Pid]
		Default.Unlock()
		if !ok {
			continue
		}
		if c.c != nil {
			// after we get an exit, call wait on the go process to make sure all
			// pipes are closed and finalizers are run on the process
			c.c.Wait()
		}
		c.ExitCh <- e.Status
		Default.Lock()
		delete(Default.cmds, e.Pid)
		Default.Unlock()
	}
	return err
}

var Default = &Monitor{
	cmds: make(map[int]*Cmd),
}

type Monitor struct {
	sync.Mutex
	cmds map[int]*Cmd
}

func (m *Monitor) Output(c *exec.Cmd) ([]byte, error) {
	var b bytes.Buffer
	c.Stdout = &b
	if err := m.Run(c); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (m *Monitor) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	var b bytes.Buffer
	c.Stdout = &b
	c.Stderr = &b
	if err := m.Run(c); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Start starts the command a registers the process with the reaper
func (m *Monitor) Start(c *exec.Cmd) error {
	rc := &Cmd{
		c:      c,
		ExitCh: make(chan int, 1),
	}
	m.Lock()
	// start the process
	if err := rc.c.Start(); err != nil {
		m.Unlock()
		return err
	}
	m.cmds[rc.c.Process.Pid] = rc
	m.Unlock()
	return nil
}

// Run runs and waits for the command to finish
func (m *Monitor) Run(c *exec.Cmd) error {
	if err := m.Start(c); err != nil {
		return err
	}
	_, err := m.Wait(c)
	return err
}

func (m *Monitor) Wait(c *exec.Cmd) (int, error) {
	return m.WaitPid(c.Process.Pid)
}

func (m *Monitor) Register(pid int, c *Cmd) {
	m.Lock()
	m.cmds[pid] = c
	m.Unlock()
}

func (m *Monitor) WaitPid(pid int) (int, error) {
	m.Lock()
	rc, ok := m.cmds[pid]
	m.Unlock()
	if !ok {
		return 255, fmt.Errorf("process does not exist")
	}
	return <-rc.ExitCh, nil
}

type Cmd struct {
	c      *exec.Cmd
	ExitCh chan int
}
