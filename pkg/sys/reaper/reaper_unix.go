//go:build !windows

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

package reaper

import (
	"container/list"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	runc "github.com/containerd/go-runc"
	"github.com/containerd/log"
	"golang.org/x/sys/unix"
)

var (
	once       sync.Once
	eventQueue = NewSafeList()
	// ErrNoSuchProcess is returned when the process no longer exists
	ErrNoSuchProcess = errors.New("no such process")
)

const bufferSize = 32

type subscriber struct {
	sync.Mutex
	c      chan runc.Exit
	closed bool
}

func (s *subscriber) close() {
	s.Lock()
	if s.closed {
		s.Unlock()
		return
	}
	close(s.c)
	s.closed = true
	s.Unlock()
}

func (s *subscriber) do(fn func()) {
	s.Lock()
	fn()
	s.Unlock()
}

type SafeList struct {
	mu    sync.Mutex
	cond  *sync.Cond
	queue *list.List
}

func NewSafeList() *SafeList {
	sl := &SafeList{
		queue: list.New(),
	}
	sl.cond = sync.NewCond(&sl.mu)
	return sl
}

func (sl *SafeList) Push(value any) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	sl.queue.PushBack(value)
	sl.cond.Signal()
}

func (sl *SafeList) Pop() any {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	for sl.queue.Len() == 0 {
		sl.cond.Wait()
	}
	elem := sl.queue.Front()
	value := elem.Value
	sl.queue.Remove(elem)
	return value
}

func notify() {
	go func() {
		for {
			event := eventQueue.Pop()
			if exitEvt, ok := event.(eventExit); ok {
				done := Default.notify(runc.Exit{
					Timestamp: exitEvt.EventTime,
					Pid:       exitEvt.Pid,
					Status:    exitEvt.Status,
				})
				select {
				case <-done:
				// TODO: remove timeout
				case <-time.After(30 * time.Second):
					log.L.Warnf("event with pid %d status:%d may be lost", exitEvt.Pid, exitEvt.Status)
				}
			}
		}
	}()
}

// Reap should be called when the process receives an SIGCHLD.  Reap will reap
// all exited processes and close their wait channels
func Reap() error {
	once.Do(notify)
	err := reap(false)
	return err
}

// Default is the default monitor initialized for the package
var Default = &Monitor{
	subscribers: make(map[chan runc.Exit]*subscriber),
}

// Monitor monitors the underlying system for process status changes
type Monitor struct {
	sync.Mutex

	subscribers map[chan runc.Exit]*subscriber
}

// Start starts the command and registers the process with the reaper
func (m *Monitor) Start(c *exec.Cmd) (chan runc.Exit, error) {
	ec := m.Subscribe()
	if err := c.Start(); err != nil {
		m.Unsubscribe(ec)
		return nil, err
	}
	return ec, nil
}

// StartLocked starts the command and registers the process with the reaper
func (m *Monitor) StartLocked(c *exec.Cmd) (chan runc.Exit, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return m.Start(c)
}

// Wait blocks until a process is signal as dead.
// User should rely on the value of the exit status to determine if the
// command was successful or not.
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

// WaitTimeout is used to skip the blocked command and kill the left process.
func (m *Monitor) WaitTimeout(c *exec.Cmd, ec chan runc.Exit, timeout time.Duration) (int, error) {
	type exitStatusWrapper struct {
		status int
		err    error
	}

	// capacity can make sure that the following goroutine will not be
	// blocked if there is no receiver when timeout.
	waitCh := make(chan *exitStatusWrapper, 1)
	go func() {
		defer close(waitCh)

		status, err := m.Wait(c, ec)
		waitCh <- &exitStatusWrapper{
			status: status,
			err:    err,
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		syscall.Kill(c.Process.Pid, syscall.SIGKILL)
		return 0, fmt.Errorf("timeout %v for cmd(pid=%d): %s, %s", timeout, c.Process.Pid, c.Path, c.Args)
	case res := <-waitCh:
		return res.status, res.err
	}
}

// Subscribe to process exit changes
func (m *Monitor) Subscribe() chan runc.Exit {
	c := make(chan runc.Exit, bufferSize)
	m.Lock()
	m.subscribers[c] = &subscriber{
		c: c,
	}
	m.Unlock()
	return c
}

// Unsubscribe to process exit changes
func (m *Monitor) Unsubscribe(c chan runc.Exit) {
	m.Lock()
	s, ok := m.subscribers[c]
	if !ok {
		m.Unlock()
		return
	}
	s.close()
	delete(m.subscribers, c)
	m.Unlock()
}

func (m *Monitor) getSubscribers() map[chan runc.Exit]*subscriber {
	out := make(map[chan runc.Exit]*subscriber)
	m.Lock()
	maps.Copy(out, m.subscribers)
	m.Unlock()
	return out
}

func (m *Monitor) notify(e runc.Exit) chan struct{} {
	const timeout = 1 * time.Millisecond
	var (
		done    = make(chan struct{}, 1)
		timer   = time.NewTimer(timeout)
		success = make(map[chan runc.Exit]struct{})
	)
	stop(timer, true)

	go func() {
		defer close(done)

		for {
			var (
				failed      int
				subscribers = m.getSubscribers()
			)
			for _, s := range subscribers {
				s.do(func() {
					if s.closed {
						return
					}
					if _, ok := success[s.c]; ok {
						return
					}
					timer.Reset(timeout)
					recv := true
					select {
					case s.c <- e:
						success[s.c] = struct{}{}
					case <-timer.C:
						recv = false
						failed++
					}
					stop(timer, recv)
				})
			}
			// all subscribers received the message
			if failed == 0 {
				return
			}
		}
	}()
	return done
}

func stop(timer *time.Timer, recv bool) {
	if !timer.Stop() && recv {
		<-timer.C
	}
}

// exit is the wait4 information from an exited process
type exit struct {
	Pid    int
	Status int
}

type eventExit struct {
	EventTime time.Time
	exit
}

// reap reaps all child processes for the calling process and returns their
// exit information
func reap(wait bool) (err error) {
	var (
		ws  unix.WaitStatus
		rus unix.Rusage
	)
	flag := unix.WNOHANG
	if wait {
		flag = 0
	}
	for {
		pid, err := unix.Wait4(-1, &ws, flag, &rus)
		if err != nil {
			if err == unix.ECHILD {
				return nil
			}
			return err
		}
		if pid <= 0 {
			return nil
		}
		eventQueue.Push(eventExit{
			EventTime: time.Now(),
			exit: exit{
				Pid:    pid,
				Status: exitStatus(ws),
			},
		})
	}
}

const exitSignalOffset = 128

// exitStatus returns the correct exit status for a process based on if it
// was signaled or exited cleanly
func exitStatus(status unix.WaitStatus) int {
	if status.Signaled() {
		return exitSignalOffset + int(status.Signal())
	}
	return status.ExitStatus()
}
