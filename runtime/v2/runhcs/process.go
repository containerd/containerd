// +build windows

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

package runhcs

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/runtime"
)

type processExit struct {
	pid        uint32
	exitStatus uint32
	exitedAt   time.Time
	exitErr    error
}

func newProcess(ctx context.Context, s *service, id string, pid uint32, pr *pipeRelay, bundle, stdin, stdout, stderr string, terminal bool) (*process, error) {
	p, err := os.FindProcess(int(pid))
	if err != nil {
		return nil, err
	}
	process := &process{
		cid:       id,
		id:        id,
		bundle:    bundle,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		terminal:  terminal,
		relay:     pr,
		waitBlock: make(chan struct{}),
	}
	go waitForProcess(ctx, process, p, s)
	return process, nil
}

func waitForProcess(ctx context.Context, process *process, p *os.Process, s *service) {
	pid := uint32(p.Pid)
	process.startedWg.Add(1)

	// Store the default non-exited value for calls to stat
	process.exit.Store(&processExit{
		pid:        pid,
		exitStatus: 255,
		exitedAt:   time.Time{},
		exitErr:    nil,
	})

	var status int
	processState, eerr := p.Wait()
	if eerr != nil {
		status = 255
		p.Kill()
	} else {
		status = processState.Sys().(syscall.WaitStatus).ExitStatus()
	}

	now := time.Now()
	process.exit.Store(&processExit{
		pid:        pid,
		exitStatus: uint32(status),
		exitedAt:   now,
		exitErr:    eerr,
	})

	// Wait for the relay
	process.relay.wait()

	// Wait for the started event to fire if it hasn't already
	process.startedWg.Wait()

	// We publish the exit before freeing upstream so that the exit event always
	// happens before any delete event.
	s.publisher.Publish(
		ctx,
		runtime.TaskExitEventTopic,
		&eventstypes.TaskExit{
			ContainerID: process.cid,
			ID:          process.id,
			Pid:         pid,
			ExitStatus:  uint32(status),
			ExitedAt:    now,
		})

	// close the client io, and free upstream waiters
	process.close()
}

func newExecProcess(ctx context.Context, s *service, cid, id string, pr *pipeRelay, bundle, stdin, stdout, stderr string, terminal bool) (*process, error) {
	process := &process{
		cid:       cid,
		id:        id,
		bundle:    bundle,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		terminal:  terminal,
		relay:     pr,
		waitBlock: make(chan struct{}),
	}
	process.startedWg.Add(1)

	// Store the default non-exited value for calls to stat
	process.exit.Store(&processExit{
		pid:        0, // This is updated when the call to Start happens and the state is overwritten in waitForProcess.
		exitStatus: 255,
		exitedAt:   time.Time{},
		exitErr:    nil,
	})
	return process, nil
}

type process struct {
	sync.Mutex

	cid string
	id  string

	bundle   string
	stdin    string
	stdout   string
	stderr   string
	terminal bool
	relay    *pipeRelay

	// started track if the process has ever been started and will not be reset
	// for the lifetime of the process object.
	started   bool
	startedWg sync.WaitGroup

	waitBlock chan struct{}
	// exit holds the exit value for all calls to `stat`. By default a
	// non-exited value is stored of status: 255, at: time 0.
	exit atomic.Value

	// closeOnce is responsible for closing waitBlock and any io.
	closeOnce sync.Once
}

// closeIO closes the stdin of the executing process to unblock any waiters
func (p *process) closeIO() {
	p.Lock()
	defer p.Unlock()

	p.relay.closeIO()
}

// close closes all stdio and frees any waiters. This is safe to call multiple
// times.
func (p *process) close() {
	p.closeOnce.Do(func() {
		p.relay.close()

		// Free any waiters
		close(p.waitBlock)
	})
}

// stat is a non-blocking query of the current process state.
func (p *process) stat() *processExit {
	er := p.exit.Load()
	return er.(*processExit)
}

// wait waits for the container process to exit and returns the exit status. If
// the process failed post start the processExit will contain the exitErr. This
// is safe to call previous to calling start().
func (p *process) wait() *processExit {
	<-p.waitBlock
	return p.stat()
}
