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

	"github.com/containerd/errdefs"
	"github.com/containerd/log"

	cio "github.com/containerd/containerd/v2/internal/cri/io"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/util"
)

// sandboxPortForward asks the sandbox controller to bridge port onto a stream pair, then
// copies between that pair and the client stream. It returns an error wrapping
// errdefs.ErrNotImplemented if the sandbox cannot forward ports.
func (c *criService) sandboxPortForward(ctx context.Context, sb sandboxstore.Sandbox, port int32, stream io.ReadWriteCloser) error {
	if !sb.Endpoint.IsValid() {
		return fmt.Errorf("sandbox %q has no streaming endpoint: %w", sb.ID, errdefs.ErrNotImplemented)
	}

	streamID := "portforward-" + util.GenerateID()

	if err := c.sandboxService.PortForwardSandbox(ctx, sb.Sandboxer, sb.ID, port, streamID); err != nil {
		return err
	}

	log.G(ctx).Infof("Executing port forwarding through sandbox %q endpoint %q", sb.ID, sb.Endpoint.Address)
	// The streaming server never cancels its context, so bound the stream goroutines here.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pfIO, err := cio.NewStreamPortForwardIO(ctx, sb.Endpoint.Address, streamID)
	if err != nil {
		return fmt.Errorf("failed to open port forward streams for sandbox %q: %w", sb.ID, err)
	}
	defer pfIO.Close()
	defer stream.Close()

	if err := copyPortForward(ctx, sb.ID, port, pfIO, stream); err != nil {
		return fmt.Errorf("failed to execute portforward through sandbox %q: %w", sb.ID, err)
	}
	log.G(ctx).Infof("Finish port forwarding for %q port %d", sb.ID, port)

	return nil
}
