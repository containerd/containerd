// +build !windows

package reaper

import (
	"bytes"
	"os/exec"
	"sync"

	"github.com/containerd/containerd/sys"
	"github.com/pkg/errors"
)

var (
	ErrNoSuchProcess = errors.New("no such process")
)

// Reap should be called when the process receives an SIGCHLD.  Reap will reap
// all exited processes and close their wait channels
func Reap() error {
	exits, err := sys.Reap(false)
	for _, e := range exits {
		Default.Lock()
		c, ok := Default.cmds[e.Pid]
		if !ok {
			Default.unknown[e.Pid] = e.Status
			Default.Unlock()
			continue
		}
		Default.Unlock()
		if c.c != nil {
			// after we get an exit, call wait on the go process to make sure all
			// pipes are closed and finalizers are run on the process
			c.c.Wait()
		}
		c.exitStatus = e.Status
		close(c.ExitCh)
	}
	return err
}

var Default = &Monitor{
	cmds:    make(map[int]*Cmd),
	unknown: make(map[int]int),
}

type Monitor struct {
	sync.Mutex

	cmds    map[int]*Cmd
	unknown map[int]int
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
	err := m.Run(c)
	return b.Bytes(), err
}

// Start starts the command a registers the process with the reaper
func (m *Monitor) Start(c *exec.Cmd) error {
	rc := &Cmd{
		c:      c,
		ExitCh: make(chan struct{}),
	}
	// start the process
	m.Lock()
	err := c.Start()
	if c.Process != nil {
		m.RegisterNL(c.Process.Pid, rc)
	}
	m.Unlock()
	return err
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
	m.RegisterNL(pid, c)
	m.Unlock()
}

// RegisterNL does not grab the lock internally
// the caller is responsible for locking the monitor
func (m *Monitor) RegisterNL(pid int, c *Cmd) {
	if status, ok := m.unknown[pid]; ok {
		delete(m.unknown, pid)
		m.cmds[pid] = c
		c.exitStatus = status
		close(c.ExitCh)
		return
	}
	m.cmds[pid] = c
}

func (m *Monitor) WaitPid(pid int) (int, error) {
	m.Lock()
	rc, ok := m.cmds[pid]
	m.Unlock()
	if !ok {
		return 255, errors.Wrapf(ErrNoSuchProcess, "pid %d", pid)
	}
	<-rc.ExitCh
	if rc.exitStatus != 0 {
		return rc.exitStatus, errors.Errorf("exit status %d", rc.exitStatus)
	}
	return rc.exitStatus, nil
}

// Command returns the registered pid for the command created
func (m *Monitor) Command(pid int) *Cmd {
	m.Lock()
	defer m.Unlock()
	return m.cmds[pid]
}

func (m *Monitor) Delete(pid int) {
	m.Lock()
	delete(m.cmds, pid)
	m.Unlock()
}

type Cmd struct {
	c          *exec.Cmd
	ExitCh     chan struct{}
	exitStatus int
}
