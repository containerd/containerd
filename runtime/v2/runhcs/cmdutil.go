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
	"bytes"
	"context"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var (
	bytesBufferPool = sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(nil)
		},
	}
)

func getBuffer() *bytes.Buffer {
	return bytesBufferPool.Get().(*bytes.Buffer)
}

func putBuffer(b *bytes.Buffer) {
	b.Reset()
	bytesBufferPool.Put(b)
}

type processExit struct {
	pid        uint32
	exitStatus uint32
	exitedAt   time.Time
	exitErr    error
}

func runCmd(ctx context.Context, c *exec.Cmd) (*processExit, error) {
	ec, startErr := startCmd(ctx, c)
	if startErr != nil {
		return nil, startErr
	}
	er, cmdErr := waitCmd(ctx, ec)
	return er, cmdErr
}

func startCmd(ctx context.Context, c *exec.Cmd) (<-chan *processExit, error) {
	if err := c.Start(); err != nil {
		return nil, err
	}
	ec := make(chan *processExit, 1)
	go func() {
		defer close(ec)

		var status int
		eerr := c.Wait()
		if eerr != nil {
			status = 255
			if exitErr, ok := eerr.(*exec.ExitError); ok {
				if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					status = ws.ExitStatus()
				}
			}
		}
		ec <- &processExit{
			pid:        uint32(c.Process.Pid),
			exitStatus: uint32(status),
			exitedAt:   time.Now(),
			exitErr:    eerr,
		}
	}()

	return ec, nil
}

func waitCmd(ctx context.Context, ec <-chan *processExit) (*processExit, error) {
	e := <-ec
	if e.exitStatus != 0 {
		return e, e.exitErr
	}
	return e, nil
}
