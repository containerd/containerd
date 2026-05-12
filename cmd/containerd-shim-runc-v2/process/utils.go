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

package process

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	runc "github.com/containerd/go-runc"
	"github.com/containerd/log"
	"golang.org/x/sys/unix"
)

const (
	// RuncRoot is the path to the root runc state directory
	RuncRoot = "/run/containerd/runc"
	// InitPidFile name of the file that contains the init pid
	InitPidFile = "init.pid"
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

// TODO(mlaventure): move to runc package?
func getLastRuntimeError(r *runc.Runc) (string, error) {
	if r.Log == "" {
		return "", nil
	}

	f, err := os.OpenFile(r.Log, os.O_RDONLY, 0400)
	if err != nil {
		return "", err
	}
	defer f.Close()

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
		strings.Contains(strings.ToLower(err.Error()), "no such process") ||
		err == unix.ESRCH {
		return fmt.Errorf("process already finished: %w", errdefs.ErrNotFound)
	} else if strings.Contains(err.Error(), "does not exist") {
		return fmt.Errorf("no such container: %w", errdefs.ErrNotFound)
	}
	return fmt.Errorf("unknown error after kill: %w", err)
}

func newPidFile(bundle string) *pidFile {
	return &pidFile{
		path: filepath.Join(bundle, InitPidFile),
	}
}

func newExecPidFile(bundle, id string) *pidFile {
	return &pidFile{
		path: filepath.Join(bundle, fmt.Sprintf("%s.pid", id)),
	}
}

type pidFile struct {
	path string
}

func (p *pidFile) Path() string {
	return p.path
}

func (p *pidFile) Read() (int, error) {
	return runc.ReadPidFile(p.path)
}

// waitTimeout handles waiting on a waitgroup with a specified timeout.
// this is commonly used for waiting on IO to finish after a process has exited
func waitTimeout(ctx context.Context, wg *sync.WaitGroup, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan struct{})
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

// drainStdioTimeout is the default budget for stdio drain in production.
// It is a package-level var (rather than a const) so tests that exercise
// the real (*Init).delete / (*execProcess).delete code paths can swap in
// a short budget to actually exercise the timeout branch. Tests must
// restore the previous value via t.Cleanup. See #12364 and #13377.
var drainStdioTimeout = 10 * time.Second

// drainAndCloseStdio waits up to `timeout` for the stdio io.CopyBuffer
// goroutines tracked by wg to finish (i.e., for buffered output to be
// copied to the downstream consumer), then closes the registered closers
// and the processIO. The drain step uses a context derived from
// context.Background() so it does NOT consume any caller-supplied ctx
// budget.
//
// This is intended to be invoked as `go drainAndCloseStdio(...)` from
// (*Init).delete and (*execProcess).delete: the synchronous portion of
// delete() returns immediately, runtime.Delete and mount.UnmountRecursive
// run with the full outer ctx, and the stdio close runs after the drain so
// the io.CopyBuffer goroutines are not forcibly terminated before they
// finish copying buffered output (the contract that #12364 was preserving).
//
// The close steps always run, even if the drain times out, so a wedged
// drain does not prevent cleanup. The timeout exists only as a backstop
// for the pathological case where the underlying read goroutines are not
// otherwise released. Production callers pass drainStdioTimeout (10s);
// tests pass a short value to actually exercise the timeout branch.
// See #12364 and #13377.
func drainAndCloseStdio(wg *sync.WaitGroup, pio *processIO, closers []io.Closer, logger *log.Entry, what string, timeout time.Duration) {
	if err := waitTimeout(context.Background(), wg, timeout); err != nil {
		logger.WithError(err).Errorf("failed to drain %s io", what)
	}
	for _, c := range closers {
		_ = c.Close()
	}
	if pio != nil {
		pio.Close()
	}
}

func stateName(v any) string {
	switch v.(type) {
	case *runningState, *execRunningState:
		return "running"
	case *createdState, *execCreatedState, *createdCheckpointState:
		return "created"
	case *pausedState:
		return "paused"
	case *deletedState:
		return "deleted"
	case *stoppedState:
		return "stopped"
	}
	panic(fmt.Errorf("invalid state %v", v))
}
