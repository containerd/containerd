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

package server

import (
	"context"
	"fmt"
	"io"
	"math"

	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/exec"

	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"k8s.io/kubelet/pkg/cri/streaming"
)

type streamRuntime struct {
	c *criService
}

func newStreamRuntime(c *criService) streaming.Runtime {
	return &streamRuntime{c: c}
}

// Exec executes a command inside the container. exec.ExitError is returned if the command
// returns non-zero exit code.
func (s *streamRuntime) Exec(ctx context.Context, containerID string, cmd []string, stdin io.Reader, stdout, stderr io.WriteCloser,
	tty bool, resize <-chan remotecommand.TerminalSize) error {
	exitCode, err := s.c.execInContainer(ctrdutil.WithNamespace(ctx), containerID, execOptions{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		tty:    tty,
		resize: resize,
	})
	if err != nil {
		return fmt.Errorf("failed to exec in container: %w", err)
	}
	if *exitCode == 0 {
		return nil
	}
	return &exec.CodeExitError{
		Err:  fmt.Errorf("error executing command %v, exit code %d", cmd, *exitCode),
		Code: int(*exitCode),
	}
}

func (s *streamRuntime) Attach(ctx context.Context, containerID string, in io.Reader, out, err io.WriteCloser, tty bool,
	resize <-chan remotecommand.TerminalSize) error {
	return s.c.attachContainer(ctrdutil.WithNamespace(ctx), containerID, in, out, err, tty, resize)
}

func (s *streamRuntime) PortForward(ctx context.Context, podSandboxID string, port int32, stream io.ReadWriteCloser) error {
	if port <= 0 || port > math.MaxUint16 {
		return fmt.Errorf("invalid port %d", port)
	}
	ctx = ctrdutil.WithNamespace(ctx)
	return s.c.portForward(ctx, podSandboxID, port, stream)
}

// handleResizing spawns a goroutine that processes the resize channel, calling resizeFunc for each
// remotecommand.TerminalSize received from the channel.
func handleResizing(ctx context.Context, resize <-chan remotecommand.TerminalSize, resizeFunc func(size remotecommand.TerminalSize)) {
	if resize == nil {
		return
	}

	go func() {
		defer runtime.HandleCrash()

		for {
			select {
			case <-ctx.Done():
				return
			case size, ok := <-resize:
				if !ok {
					return
				}
				if size.Height < 1 || size.Width < 1 {
					continue
				}
				resizeFunc(size)
			}
		}
	}()
}
