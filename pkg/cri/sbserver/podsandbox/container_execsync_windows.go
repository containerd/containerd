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

package podsandbox

import (
	"context"
	"fmt"
	"io"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/oci"
	cio "github.com/containerd/containerd/pkg/cri/io"
	"github.com/containerd/containerd/pkg/cri/util"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/remotecommand"
)

// execOptions specifies how to execute command in container.
type execOptions struct {
	cmd     []string
	stdin   io.Reader
	stdout  io.WriteCloser
	stderr  io.WriteCloser
	tty     bool
	resize  <-chan remotecommand.TerminalSize
	timeout time.Duration
}

func (c *Controller) execInternal(ctx context.Context, container containerd.Container, id string, opts execOptions) (*uint32, error) {
	// Cancel the context before returning to ensure goroutines are stopped.
	// This is important, because if `Start` returns error, `Wait` will hang
	// forever unless we cancel the context.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	spec, err := container.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec: %w", err)
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load task: %w", err)
	}
	pspec := spec.Process

	pspec.Terminal = opts.tty
	if opts.tty {
		if err := oci.WithEnv([]string{"TERM=xterm"})(ctx, nil, nil, spec); err != nil {
			return nil, fmt.Errorf("add TERM env var to spec: %w", err)
		}
	}

	pspec.Args = opts.cmd

	if opts.stdout == nil {
		opts.stdout = cio.NewDiscardLogger()
	}
	if opts.stderr == nil {
		opts.stderr = cio.NewDiscardLogger()
	}
	execID := util.GenerateID()
	log.G(ctx).Debugf("Generated exec id %q for container %q", execID, id)
	volatileRootDir := c.getVolatileContainerRootDir(id)
	var execIO *cio.ExecIO
	process, err := task.Exec(ctx, execID, pspec,
		func(id string) (containerdio.IO, error) {
			var err error
			execIO, err = cio.NewExecIO(id, volatileRootDir, opts.tty, opts.stdin != nil)
			return execIO, err
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec %q: %w", execID, err)
	}
	defer func() {
		deferCtx, deferCancel := ctrdutil.DeferContext()
		defer deferCancel()
		if _, err := process.Delete(deferCtx, containerd.WithProcessKill); err != nil {
			log.G(ctx).WithError(err).Errorf("Failed to delete exec process %q for container %q", execID, id)
		}
	}()

	exitCh, err := process.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for process %q: %w", execID, err)
	}
	if err := process.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start exec %q: %w", execID, err)
	}

	handleResizing(ctx, opts.resize, func(size remotecommand.TerminalSize) {
		if err := process.Resize(ctx, uint32(size.Width), uint32(size.Height)); err != nil {
			log.G(ctx).WithError(err).Errorf("Failed to resize process %q console for container %q", execID, id)
		}
	})

	attachDone := execIO.Attach(cio.AttachOptions{
		Stdin:     opts.stdin,
		Stdout:    opts.stdout,
		Stderr:    opts.stderr,
		Tty:       opts.tty,
		StdinOnce: true,
		CloseStdin: func() error {
			return process.CloseIO(ctx, containerd.WithStdinCloser)
		},
	})

	execCtx := ctx
	if opts.timeout > 0 {
		var execCtxCancel context.CancelFunc
		execCtx, execCtxCancel = context.WithTimeout(ctx, opts.timeout)
		defer execCtxCancel()
	}

	select {
	case <-execCtx.Done():
		// Ignore the not found error because the process may exit itself before killing.
		if err := process.Kill(ctx, syscall.SIGKILL); err != nil && !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to kill exec %q: %w", execID, err)
		}
		// Wait for the process to be killed.
		exitRes := <-exitCh
		log.G(ctx).Debugf("Timeout received while waiting for exec process kill %q code %d and error %v",
			execID, exitRes.ExitCode(), exitRes.Error())
		<-attachDone
		log.G(ctx).Debugf("Stream pipe for exec process %q done", execID)
		return nil, fmt.Errorf("timeout %v exceeded: %w", opts.timeout, execCtx.Err())
	case exitRes := <-exitCh:
		code, _, err := exitRes.Result()
		log.G(ctx).Debugf("Exec process %q exits with exit code %d and error %v", execID, code, err)
		if err != nil {
			return nil, fmt.Errorf("failed while waiting for exec %q: %w", execID, err)
		}
		<-attachDone
		log.G(ctx).Debugf("Stream pipe for exec process %q done", execID)
		return &code, nil
	}
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
