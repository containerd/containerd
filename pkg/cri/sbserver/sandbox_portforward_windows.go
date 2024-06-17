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

package sbserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"k8s.io/utils/exec"

	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	cioutil "github.com/containerd/containerd/pkg/ioutil"
	"github.com/containerd/log"
)

func (c *criService) portForward(ctx context.Context, id string, port int32, stream io.ReadWriter) error {
	sandbox, err := c.sandboxStore.Get(id)
	if err != nil {
		return fmt.Errorf("failed to find sandbox %q in store: %w", id, err)
	}
	// host process containers
	if hostNetwork(sandbox.Config) {
		return hpcPortForwarding(ctx, id, port, stream)
	}

	stdout := cioutil.NewNopWriteCloser(stream)
	stderrBuffer := new(bytes.Buffer)
	stderr := cioutil.NewNopWriteCloser(stderrBuffer)
	// localhost is resolved to 127.0.0.1 in ipv4, and ::1 in ipv6.
	// Explicitly using ipv4 IP address in here to avoid flakiness.
	cmd := []string{"wincat.exe", "127.0.0.1", fmt.Sprint(port)}
	err = c.execInSandbox(ctx, id, cmd, stream, stdout, stderr)
	if err != nil {
		return fmt.Errorf("failed to execute port forward in sandbox: %s: %w", stderrBuffer.String(), err)
	}
	return nil
}

func (c *criService) execInSandbox(ctx context.Context, sandboxID string, cmd []string, stdin io.Reader, stdout, stderr io.WriteCloser) error {
	// Get sandbox from our sandbox store.
	sb, err := c.sandboxStore.Get(sandboxID)
	if err != nil {
		return fmt.Errorf("failed to find sandbox %q in store: %w", sandboxID, err)
	}

	// Check the sandbox state
	state := sb.Status.Get().State
	if state != sandboxstore.StateReady {
		return fmt.Errorf("sandbox is in %s state", fmt.Sprint(state))
	}

	opts := execOptions{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		tty:    false,
		resize: nil,
	}
	exitCode, err := c.execInternal(ctx, sb.Container, sandboxID, opts)
	if err != nil {
		return fmt.Errorf("failed to exec in sandbox: %w", err)
	}
	if *exitCode == 0 {
		return nil
	}
	return &exec.CodeExitError{
		Err:  fmt.Errorf("error executing command %v, exit code %d", cmd, *exitCode),
		Code: int(*exitCode),
	}
}

func hpcPortForwarding(ctx context.Context, id string, port int32, stream io.ReadWriter) error {
	// Dial to localhost for HPCs since host process containers
	// share network namespace of the host
	podIP := "localhost"
	err := func() error {
		conn, err := net.Dial("tcp", net.JoinHostPort(podIP, fmt.Sprintf("%d", port)))
		if err != nil {
			return fmt.Errorf("failed to connect to %s:%d for pod %q: %v", podIP, port, id, err)
		}
		log.G(ctx).Debugf("Connection to ip %s and port %d was successful", podIP, port)

		defer conn.Close()

		// copy stream
		errCh := make(chan error, 2)
		// Copy from the namespace port connection to the client stream
		go func() {
			log.G(ctx).Debugf("PortForward copying data from namespace %q port %d to the client stream", id, port)
			_, err := io.Copy(stream, conn)
			errCh <- err
		}()

		// Copy from the client stream to the namespace port connection
		go func() {
			log.G(ctx).Debugf("PortForward copying data from client stream to namespace %q port %d", id, port)
			_, err := io.Copy(conn, stream)
			errCh <- err
		}()

		// Wait until the first error is returned by one of the connections
		// we use errFwd to store the result of the port forwarding operation
		// if the context is cancelled close everything and return
		var errFwd error
		select {
		case errFwd = <-errCh:
			log.G(ctx).Debugf("PortForward stop forwarding in one direction in network namespace %q port %d: %v", id, port, errFwd)
		case <-ctx.Done():
			log.G(ctx).Debugf("PortForward cancelled in network namespace %q port %d: %v", id, port, ctx.Err())
			return ctx.Err()
		}
		// give a chance to terminate gracefully or timeout
		// after 1s
		const timeout = time.Second
		select {
		case e := <-errCh:
			if errFwd == nil {
				errFwd = e
			}
			log.G(ctx).Debugf("PortForward stopped forwarding in both directions in network namespace %q port %d: %v", id, port, e)
		case <-time.After(timeout):
			log.G(ctx).Debugf("PortForward timed out waiting to close the connection in network namespace %q port %d", id, port)
		case <-ctx.Done():
			log.G(ctx).Debugf("PortForward cancelled in network namespace %q port %d: %v", id, port, ctx.Err())
			errFwd = ctx.Err()
		}

		return errFwd
	}()

	if err != nil {
		return fmt.Errorf("failed to execute portforward for HPC podId %v, podIp %v, err: %w", id, podIP, err)
	}
	log.G(ctx).Debugf("Finish port forwarding for windows %q port %d", id, port)

	return nil
}
