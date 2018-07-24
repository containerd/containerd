// +build !windows

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package shim

import (
	"os/exec"
	"sync"

	"github.com/containerd/containerd/sys"
	runc "github.com/containerd/go-runc"
)

const bufferSize = 32

func init() {
	Default = newMonitor()
}

type defaultSub struct {
	c chan sys.Exit
}

func (s *defaultSub) send(e sys.Exit) {
	s.c <- e
}

func (s *defaultSub) Recv() <-chan sys.Exit {
	return s.c
}

func (s *defaultSub) Close() {
	close(s.c)
}

func newMonitor() *unixMonitor {
	return &unixMonitor{
		subscribers: make(map[Subscriber]struct{}),
	}
}

// unixMonitor monitors the underlying system for process status changes
type unixMonitor struct {
	sync.Mutex

	subscribers map[Subscriber]struct{}
}

// Reap should be called when the process receives an SIGCHLD.  Reap will reap
// all exited processes and close their wait channels
func (m *unixMonitor) Reap() error {
	exits, err := sys.Reap(false)
	m.Lock()
	for c := range m.subscribers {
		for _, e := range exits {
			c.send(sys.Exit{
				Time:   e.Time,
				Pid:    e.Pid,
				Status: e.Status,
			})
		}

	}
	m.Unlock()
	return err
}

// Start starts the command a registers the process with the reaper
func (m *unixMonitor) Start(c *exec.Cmd) (Subscriber, error) {
	ec := m.Subscribe()
	if err := c.Start(); err != nil {
		m.Unsubscribe(ec)
		return nil, err
	}
	return ec, nil
}

// Wait blocks until a process is signal as dead.
// User should rely on the value of the exit status to determine if the
// command was successful or not.
func (m *unixMonitor) Wait(c *exec.Cmd, s Subscriber) (int, error) {
	for e := range s.Recv() {
		if e.Pid == c.Process.Pid {
			// make sure we flush all IO
			c.Wait()
			m.Unsubscribe(s)
			return e.Status, nil
		}
	}
	// return no such process if the ec channel is closed and no more exit
	// events will be sent
	return -1, ErrNoSuchProcess
}

// Subscribe to process exit changes
func (m *unixMonitor) Subscribe() Subscriber {
	c := make(chan sys.Exit, bufferSize)
	s := &defaultSub{c: c}
	m.Lock()
	m.subscribers[s] = struct{}{}
	m.Unlock()
	return s
}

// Unsubscribe to process exit changes
func (m *unixMonitor) Unsubscribe(s Subscriber) {
	m.Lock()
	delete(m.subscribers, s)
	s.Close()
	m.Unlock()
}

// RuncMonitor returns a go-runc monitor
func (m *unixMonitor) RuncMonitor() runc.ProcessMonitor {
	return &runcMonitor{
		m:  m,
		cs: make(map[chan runc.Exit]*runcSub),
	}
}

type runcSub struct {
	c chan runc.Exit
}

func (s *runcSub) send(e sys.Exit) {
	s.c <- runc.Exit{
		Timestamp: e.Time,
		Status:    e.Status,
		Pid:       e.Pid,
	}
}

func (s *runcSub) Recv() <-chan sys.Exit {
	return nil
}

func (s *runcSub) Close() {
	close(s.c)
}

type runcMonitor struct {
	sync.Mutex

	m  *unixMonitor
	cs map[chan runc.Exit]*runcSub
}

// Start starts the command a registers the process with the reaper
func (m *runcMonitor) Start(c *exec.Cmd) (chan runc.Exit, error) {
	ec := make(chan runc.Exit, bufferSize)
	s := &runcSub{c: ec}
	m.m.Lock()
	m.m.subscribers[s] = struct{}{}
	m.Lock()
	m.cs[ec] = s
	m.Unlock()
	m.m.Unlock()
	if err := c.Start(); err != nil {
		m.m.Unsubscribe(s)
		return nil, err
	}
	return ec, nil
}

// Wait blocks until a process is signal as dead.
// User should rely on the value of the exit status to determine if the
// command was successful or not.
func (m *runcMonitor) Wait(c *exec.Cmd, ec chan runc.Exit) (int, error) {
	for e := range ec {
		if e.Pid == c.Process.Pid {
			// make sure we flush all IO
			c.Wait()
			m.Lock()
			s := m.cs[ec]
			delete(m.cs, ec)
			m.Unlock()
			m.m.Unsubscribe(s)
			return e.Status, nil
		}
	}
	// return no such process if the ec channel is closed and no more exit
	// events will be sent
	return -1, ErrNoSuchProcess
}
