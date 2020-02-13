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

package proc

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd/errdefs"
	runc "github.com/containerd/go-runc"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// safePid is a thread safe wrapper for pid.
type safePid struct {
	sync.Mutex
	pid int
}

func (s *safePid) get() int {
	s.Lock()
	defer s.Unlock()
	return s.pid
}

type atomicBool int32

func (ab *atomicBool) set(b bool) {
	if b {
		atomic.StoreInt32((*int32)(ab), 1)
	} else {
		atomic.StoreInt32((*int32)(ab), 0)
	}
}

func (ab *atomicBool) get() bool {
	return atomic.LoadInt32((*int32)(ab)) == 1
}

// TODO(mlaventure): move to runc package?
func getLastRuntimeError(r *runc.Runc) (string, error) {
	if r.Log == "" {
		return "", nil
	}

	f, err := os.OpenFile(r.Log, os.O_RDONLY, 0400)
	if err != nil {
		return "", err
	}

	var (
		errMsg string
		log    struct {
			Level string
			Msg   string
			Time  time.Time
		}
	)

	dec := json.NewDecoder(f)
	for err = nil; err == nil; {
		if err = dec.Decode(&log); err != nil && err != io.EOF {
			return "", err
		}
		if log.Level == "error" {
			errMsg = strings.TrimSpace(log.Msg)
		}
	}

	return errMsg, nil
}

// criuError returns only the first line of the error message from criu
// it tries to add an invalid dump log location when returning the message
func criuError(err error) string {
	parts := strings.Split(err.Error(), "\n")
	return parts[0]
}

func copyFile(to, from string) error {
	ff, err := os.Open(from)
	if err != nil {
		return err
	}
	defer ff.Close()
	tt, err := os.Create(to)
	if err != nil {
		return err
	}
	defer tt.Close()

	p := bufPool.Get().(*[]byte)
	defer bufPool.Put(p)
	_, err = io.CopyBuffer(tt, ff, *p)
	return err
}

func checkKillError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "os: process already finished") ||
		strings.Contains(err.Error(), "container not running") ||
		err == unix.ESRCH {
		return errors.Wrapf(errdefs.ErrNotFound, "process already finished")
	}
	return errors.Wrapf(err, "unknown error after kill")
}

func hasNoIO(r *CreateConfig) bool {
	return r.Stdin == "" && r.Stdout == "" && r.Stderr == ""
}

// waitTimeout handles waiting on a waitgroup with a specified timeout.
// this is commonly used for waiting on IO to finish after a process has exited
func waitTimeout(ctx context.Context, wg *sync.WaitGroup, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan struct{}, 1)
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
