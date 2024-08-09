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
	"bytes"
	"context"
	"fmt"
	"io"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"k8s.io/client-go/tools/remotecommand"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/internal/cri/config"
	cio "github.com/containerd/containerd/v2/internal/cri/io"
	"github.com/containerd/containerd/v2/internal/cri/util"
	containerdio "github.com/containerd/containerd/v2/pkg/cio"
	cioutil "github.com/containerd/containerd/v2/pkg/ioutil"
)

type cappedWriter struct {
	w      io.WriteCloser
	remain int
}

func (cw *cappedWriter) Write(p []byte) (int, error) {
	if cw.remain <= 0 {
		return len(p), nil
	}

	end := cw.remain
	if end > len(p) {
		end = len(p)
	}
	written, err := cw.w.Write(p[0:end])
	cw.remain -= written

	if err != nil {
		return written, err
	}
	return len(p), nil
}

func (cw *cappedWriter) Close() error {
	return cw.w.Close()
}

func (cw *cappedWriter) isFull() bool {
	return cw.remain <= 0
}

// ExecSync executes a command in the container, and returns the stdout output.
// If command exits with a non-zero exit code, an error is returned.
func (c *criService) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (*runtime.ExecSyncResponse, error) {
	const maxStreamSize = 1024 * 1024 * 16

	var stdout, stderr bytes.Buffer

	// cappedWriter truncates the output. In that case, the size of
	// the ExecSyncResponse will hit the CRI plugin's gRPC response limit.
	// Thus the callers outside of the containerd process (e.g. Kubelet) never see
	// the truncated output.
	cout := &cappedWriter{w: cioutil.NewNopWriteCloser(&stdout), remain: maxStreamSize}
	cerr := &cappedWriter{w: cioutil.NewNopWriteCloser(&stderr), remain: maxStreamSize}

	exitCode, err := c.execInContainer(ctx, r.GetContainerId(), execOptions{
		cmd:     r.GetCmd(),
		stdout:  cout,
		stderr:  cerr,
		timeout: time.Duration(r.GetTimeout()) * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec in container: %w", err)
	}

	return &runtime.ExecSyncResponse{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: int32(*exitCode),
	}, nil
}

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

func (c *criService) execInternal(ctx context.Context, container containerd.Container, id string, opts execOptions) (*uint32, error) {
	// Cancel the context before returning to ensure goroutines are stopped.
	// This is important, because if `Start` returns error, `Wait` will hang
	// forever unless we cancel the context.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var drainExecSyncIOTimeout time.Duration
	var err error

	if c.config.DrainExecSyncIOTimeout != "" {
		drainExecSyncIOTimeout, err = time.ParseDuration(c.config.DrainExecSyncIOTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to parse drain_exec_sync_io_timeout %q: %w",
				c.config.DrainExecSyncIOTimeout, err)
		}
	}

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
	// CommandLine may already be set on the container's spec, but we want to only use Args here.
	pspec.CommandLine = ""

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
			cntr, err := c.containerStore.Get(container.ID())
			if err != nil {
				return nil, fmt.Errorf("an error occurred when try to find container %q: %w", container.ID(), err)
			}
			sb, err := c.sandboxStore.Get(cntr.SandboxID)
			if err != nil {
				return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %w", cntr.SandboxID, err)
			}
			ociRuntime, err := c.config.GetSandboxRuntime(sb.Config, sb.Metadata.RuntimeHandler)
			if err != nil {
				return nil, fmt.Errorf("failed to get sandbox runtime: %w", err)
			}
			switch ociRuntime.IOType {
			case config.IOTypeStreaming:
				execIO, err = cio.NewStreamExecIO(id, sb.Endpoint.Address, opts.tty, opts.stdin != nil)
			default:
				execIO, err = cio.NewFifoExecIO(id, volatileRootDir, opts.tty, opts.stdin != nil)
			}

			return execIO, err
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec %q: %w", execID, err)
	}
	defer func() {
		deferCtx, deferCancel := util.DeferContext()
		defer deferCancel()
		if _, err := process.Delete(deferCtx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
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

		if err := drainExecSyncIO(ctx, process, drainExecSyncIOTimeout, attachDone); err != nil {
			log.G(ctx).WithError(err).Warnf("failed to drain exec process %q io", execID)
		}

		return nil, fmt.Errorf("timeout %v exceeded: %w", opts.timeout, execCtx.Err())
	case exitRes := <-exitCh:
		code, _, err := exitRes.Result()
		log.G(ctx).Debugf("Exec process %q exits with exit code %d and error %v", execID, code, err)
		if err != nil {
			return nil, fmt.Errorf("failed while waiting for exec %q: %w", execID, err)
		}

		if err := drainExecSyncIO(ctx, process, drainExecSyncIOTimeout, attachDone); err != nil {
			return nil, fmt.Errorf("failed to drain exec process %q io: %w", execID, err)
		}
		return &code, nil
	}
}

// execInContainer executes a command inside the container synchronously, and
// redirects stdio stream properly.
// This function only returns when the exec process exits, this means that:
// 1) As long as the exec process is running, the goroutine in the cri plugin
// will be running and wait for the exit code;
// 2) `kubectl exec -it` will hang until the exec process exits, even after io
// is detached. This is different from dockershim, which leaves the exec process
// running in background after io is detached.
// https://github.com/kubernetes/kubernetes/blob/v1.15.0/pkg/kubelet/dockershim/exec.go#L127
// For example, if the `kubectl exec -it` process is killed, IO will be closed. In
// this case, the CRI plugin will still have a goroutine waiting for the exec process
// to exit and log the exit code, but dockershim won't.
func (c *criService) execInContainer(ctx context.Context, id string, opts execOptions) (*uint32, error) {
	span := tracing.SpanFromContext(ctx)
	// Get container from our container store.
	cntr, err := c.containerStore.Get(id)

	if err != nil {
		return nil, fmt.Errorf("failed to find container %q in store: %w", id, err)
	}
	id = cntr.ID
	span.SetAttributes(tracing.Attribute("container.id", id))

	state := cntr.Status.Get().State()
	if state != runtime.ContainerState_CONTAINER_RUNNING {
		return nil, fmt.Errorf("container is in %s state", criContainerStateToString(state))
	}

	return c.execInternal(ctx, cntr.Container, id, opts)
}

// drainExecSyncIO drains process IO with timeout after exec init process exits.
//
// By default, the child processes spawned by exec process will inherit standard
// io file descriptors. The shim server creates a pipe as data channel. Both
// exec process and its children write data into the write end of the pipe.
// And the shim server will read data from the pipe. If the write end is still
// open, the shim server will continue to wait for data from pipe.
//
// If the exec command is like `bash -c "sleep 365d &"`, the exec process
// is bash and quit after create `sleep 365d`. But the `sleep 365d` will hold
// the write end of the pipe for a year! It doesn't make senses that CRI plugin
// should wait for it.
func drainExecSyncIO(ctx context.Context, execProcess containerd.Process, drainExecIOTimeout time.Duration, attachDone <-chan struct{}) error {
	var timerCh <-chan time.Time

	if drainExecIOTimeout != 0 {
		timer := time.NewTimer(drainExecIOTimeout)
		defer timer.Stop()

		timerCh = timer.C
	}

	select {
	case <-timerCh:
	case <-attachDone:
		log.G(ctx).Tracef("Stream pipe for exec process %q done", execProcess.ID())
		return nil
	}

	log.G(ctx).Debugf("Exec process %q exits but the io is still held by other processes. Trying to delete exec process to release io", execProcess.ID())
	_, err := execProcess.Delete(ctx, containerd.WithProcessKill)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to release exec io by deleting exec process %q: %w",
				execProcess.ID(), err)
		}
	}
	return fmt.Errorf("failed to drain exec process %q io in %s because io is still held by other processes",
		execProcess.ID(), drainExecIOTimeout)
}
