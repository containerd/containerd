// +build !windows

package reaper

import (
	"bytes"
	"os/exec"
	"sync"
	"time"

	"github.com/containerd/containerd/sys"
	runc "github.com/containerd/go-runc"
	"github.com/pkg/errors"
)

var ErrNoSuchProcess = errors.New("no such process")

const bufferSize = 2048

// Reap should be called when the process receives an SIGCHLD.  Reap will reap
// all exited processes and close their wait channels
func Reap() error {
	now := time.Now()
	exits, err := sys.Reap(false)
	Default.Lock()
	for c := range Default.subscribers {
		for _, e := range exits {
			c <- runc.Exit{
				Timestamp: now,
				Pid:       e.Pid,
				Status:    e.Status,
			}
		}

	}
	Default.Unlock()
	return err
}

var Default = &Monitor{
	subscribers: make(map[chan runc.Exit]struct{}),
}

type Monitor struct {
	sync.Mutex

	subscribers map[chan runc.Exit]struct{}
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
func (m *Monitor) Start(c *exec.Cmd) (chan runc.Exit, error) {
	ec := m.Subscribe()
	if err := c.Start(); err != nil {
		m.Unsubscribe(ec)
		return nil, err
	}
	return ec, nil
}

// Run runs and waits for the command to finish
func (m *Monitor) Run(c *exec.Cmd) error {
	ec, err := m.Start(c)
	if err != nil {
		return err
	}
	_, err = m.Wait(c, ec)
	return err
}

func (m *Monitor) Wait(c *exec.Cmd, ec chan runc.Exit) (int, error) {
	for e := range ec {
		if e.Pid == c.Process.Pid {
			// make sure we flush all IO
			c.Wait()
			m.Unsubscribe(ec)
			return e.Status, nil
		}
	}
	// return no such process if the ec channel is closed and no more exit
	// events will be sent
	return -1, ErrNoSuchProcess
}

func (m *Monitor) Subscribe() chan runc.Exit {
	c := make(chan runc.Exit, bufferSize)
	m.Lock()
	m.subscribers[c] = struct{}{}
	m.Unlock()
	return c
}

func (m *Monitor) Unsubscribe(c chan runc.Exit) {
	m.Lock()
	delete(m.subscribers, c)
	close(c)
	m.Unlock()
}
