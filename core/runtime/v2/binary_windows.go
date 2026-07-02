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

package v2

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/containerd/log"
)

// stderrCapture buffers writes until MirrorTo is called, then flushes the
// buffered prefix to the mirror writer and forwards all subsequent writes
// there. The buffered prefix is retained for error messages.
//
// This is used for the shim helper's stderr: during startup we need to
// capture any error output; once we receive a good bootstrap response the
// helper (if it self-daemonized) will stay alive and produce output
// indefinitely, so we mirror to os.Stderr rather than buffering forever.
//
// Write is safe to call concurrently with MirrorTo because exec's internal
// stderr-drain goroutine calls Write from a separate goroutine.
type stderrCapture struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	mirror io.Writer
}

func (s *stderrCapture) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mirror != nil {
		return s.mirror.Write(p)
	}
	return s.buf.Write(p)
}

// MirrorTo flushes any buffered bytes to w and forwards all future writes
// there. After this returns, the buffer is no longer retained.
func (s *stderrCapture) MirrorTo(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buf.Len() > 0 {
		_, _ = w.Write(s.buf.Bytes())
		s.buf.Reset()
	}
	s.mirror = w
}

func (s *stderrCapture) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// readShimBootstrap starts cmd and collects the shim "start" helper's
// bootstrap response.
//
// On Windows we support self-daemonizing shims (Detachable): the helper
// writes its result, closes stdout to signal readiness, and stays alive as
// the long-lived TTRPC server. CombinedOutput would block indefinitely in
// that case. Instead we race two goroutines — one draining stdout, one
// waiting on the process — and proceed as soon as either delivers enough
// information to make a decision.
//
// The helper's exit code is not the source of truth for shim health; the
// TTRPC connection attempted by the caller is.
//
// We use os.Pipe rather than cmd.StdoutPipe so that cmd.Wait does not close
// the read end while ReadAll is still in flight (per Go docs, calling Wait
// before StdoutPipe reads complete is incorrect).
func readShimBootstrap(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("shim start pipe: %w", err)
	}
	cmd.Stdout = w
	var sc stderrCapture
	cmd.Stderr = &sc
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, fmt.Errorf("start shim: %w", err)
	}
	// Close the parent's write end; the child has its own dup. This ensures
	// ReadAll sees EOF when the child closes its copy (or exits).
	w.Close()

	type readResult struct {
		data []byte
		err  error
	}
	readCh := make(chan readResult, 1)
	go func() {
		defer r.Close()
		data, err := io.ReadAll(r)
		readCh <- readResult{data, err}
	}()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case rr := <-readCh:
		response := bytes.TrimSpace(rr.data)
		if len(response) == 0 || rr.err != nil {
			// Empty or broken stdout: pull the exit code to build a useful
			// error. Bound the wait in case the helper closed stdout but is
			// still alive (broken self-daemonizing shim).
			select {
			case waitErr := <-waitCh:
				return nil, fmt.Errorf("shim start: %w (read: %v)\nstderr: %s",
					waitErr, rr.err, sc.String())
			case <-time.After(5 * time.Second):
				cmd.Process.Kill() //nolint:errcheck
				<-waitCh
				return nil, fmt.Errorf("shim start: empty bootstrap and helper did not exit within 5s"+
					"\nread err: %v\nstderr: %s", rr.err, sc.String())
			}
		}
		// Good response. If the helper self-daemonized it will keep running
		// and producing output; mirror its stderr to containerd's own stderr
		// rather than buffering indefinitely.
		sc.MirrorTo(os.Stderr)
		go func() {
			if waitErr := <-waitCh; waitErr != nil {
				log.G(ctx).WithError(waitErr).Debug(
					"shim start helper exited with error after writing bootstrap")
			}
		}()
		return response, nil

	case waitErr := <-waitCh:
		// Helper exited before stdout finished draining. Collect whatever it
		// managed to write.
		rr := <-readCh
		response := bytes.TrimSpace(rr.data)
		if len(response) == 0 {
			return nil, fmt.Errorf("shim start: %w\nstderr: %s",
				waitErr, sc.String())
		}
		if waitErr != nil {
			return nil, fmt.Errorf("shim start: %w\nstdout: %s\nstderr: %s",
				waitErr, response, sc.String())
		}
		// Clean exit with output: classic shim happy path.
		return response, nil

	}
}
