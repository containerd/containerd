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
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

// PortForward prepares a streaming endpoint to forward ports from a PodSandbox, and returns the address.
func (c *criService) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (retRes *runtime.PortForwardResponse, retErr error) {
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox %q: %w", r.GetPodSandboxId(), err)
	}
	if sandbox.Status.Get().State != sandboxstore.StateReady {
		return nil, errors.New("sandbox container is not running")
	}
	// TODO(random-liu): Verify that ports are exposed.
	return c.streamServer.GetPortForward(r)
}

// portForward forwards a stream to the given port in the sandbox, through the sandbox
// controller or, by default, from the sandbox network namespace on the host. A sandbox
// that cannot forward ports itself falls back to the host path.
func (c *criService) portForward(ctx context.Context, id string, port int32, stream io.ReadWriteCloser) error {
	sb, err := c.sandboxStore.Get(id)
	if err != nil {
		return fmt.Errorf("failed to find sandbox %q in store: %w", id, err)
	}
	// A runtime handler that is no longer configured keeps the host behavior.
	ociRuntime, err := c.config.GetSandboxRuntime(sb.Config, sb.Metadata.RuntimeHandler)
	if err != nil {
		log.G(ctx).WithError(err).Warnf("Failed to get sandbox runtime for %q, forwarding from the host network namespace", id)
	} else if ociRuntime.PortForwardType == criconfig.PortForwardTypeSandbox {
		err := c.sandboxPortForward(ctx, sb, port, stream)
		if err == nil || !errdefs.IsNotImplemented(err) {
			return err
		}
		log.G(ctx).WithError(err).Warnf("Sandbox %q cannot forward ports, falling back to the host network namespace", id)
	}

	return c.hostPortForward(ctx, sb, port, stream)
}

// copyPortForward copies between the client stream and conn until both directions stop,
// one direction stops and the other does not follow within a grace period, or ctx is
// cancelled. A copy still in flight ends when the caller tears down conn and stream.
func copyPortForward(ctx context.Context, id string, port int32, conn io.ReadWriter, stream io.ReadWriter) error {
	errCh := make(chan error, 2)
	go func() {
		log.G(ctx).Debugf("PortForward copying data from sandbox %q port %d to the client stream", id, port)
		_, err := io.Copy(stream, conn)
		errCh <- err
	}()

	go func() {
		log.G(ctx).Debugf("PortForward copying data from client stream to sandbox %q port %d", id, port)
		_, err := io.Copy(conn, stream)
		errCh <- err
	}()

	var errFwd error
	select {
	case errFwd = <-errCh:
		log.G(ctx).Debugf("PortForward stop forwarding in one direction for sandbox %q port %d: %v", id, port, errFwd)
	case <-ctx.Done():
		log.G(ctx).Debugf("PortForward cancelled for sandbox %q port %d: %v", id, port, ctx.Err())
		return ctx.Err()
	}
	// https://linux.die.net/man/1/socat
	const timeout = time.Second
	select {
	case e := <-errCh:
		if errFwd == nil {
			errFwd = e
		}
		log.G(ctx).Debugf("PortForward stopped forwarding in both directions for sandbox %q port %d: %v", id, port, e)
	case <-time.After(timeout):
		log.G(ctx).Debugf("PortForward timed out waiting to close the connection for sandbox %q port %d", id, port)
	case <-ctx.Done():
		log.G(ctx).Debugf("PortForward cancelled for sandbox %q port %d: %v", id, port, ctx.Err())
		errFwd = ctx.Err()
	}

	return errFwd
}
