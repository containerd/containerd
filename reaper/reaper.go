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
func Reap() ([]utils.Exit, error) {
	exits, err := utils.Reap(false)
	for _, e := range exits {
		Default.mu.Lock()
		c, ok := Default.cmds[e.Pid]
		Default.mu.Unlock()
		if !ok {
			continue
		}
		// after we get an exit, call wait on the go process to make sure all
		// pipes are closed and finalizers are run on the process
		c.c.Wait()
		c.exitCh <- e.Status
		Default.mu.Lock()
		delete(Default.cmds, e.Pid)
		Default.mu.Unlock()
	}
	return exits, err
}

var Default = &Monitor{
	cmds: make(map[int]*cmd),
}

type Monitor struct {
	mu   sync.Mutex
	cmds map[int]*cmd
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
	rc := &cmd{
		c:      c,
		exitCh: make(chan int, 1),
	}
	m.mu.Lock()
	// start the process
	if err := rc.c.Start(); err != nil {
		m.mu.Unlock()
		return err
	}
	m.cmds[rc.c.Process.Pid] = rc
	m.mu.Unlock()
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
	m.mu.Lock()
	rc, ok := m.cmds[c.Process.Pid]
	m.mu.Unlock()
	if !ok {
		return 255, fmt.Errorf("process does not exist")
	}
	return <-rc.exitCh, nil
}

type cmd struct {
	c      *exec.Cmd
	exitCh chan int
}
