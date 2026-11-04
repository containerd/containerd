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

package client

import (
	"context"

	"github.com/containerd/containerd/v2/core/streaming"
	streamproxy "github.com/containerd/containerd/v2/core/streaming/proxy"
	"github.com/containerd/containerd/v2/core/transfer"
	"github.com/containerd/containerd/v2/core/transfer/proxy"
)

// Transfer executes a transfer operation between a source (src) and a destination (dest).
//
// Transfer operations execute on the daemon side within a client lease and support streaming
// callbacks for progress and credentials.
//
// Standard source and destination pairings:
//   - Pull: [github.com/containerd/containerd/v2/core/transfer/registry.OCIRegistry] to [github.com/containerd/containerd/v2/core/transfer/image.Store]
//   - Push: [github.com/containerd/containerd/v2/core/transfer/image.Store] to [github.com/containerd/containerd/v2/core/transfer/registry.OCIRegistry]
//   - Import: [github.com/containerd/containerd/v2/core/transfer/archive.ImageImportStream] to [github.com/containerd/containerd/v2/core/transfer/image.Store]
//   - Export: [github.com/containerd/containerd/v2/core/transfer/image.Store] to [github.com/containerd/containerd/v2/core/transfer/archive.ImageExportStream]
//
// Callers can provide [transfer.WithProgress] to track transfer progress events.
func (c *Client) Transfer(ctx context.Context, src any, dest any, opts ...transfer.Opt) error {
	ctx, done, err := c.WithLease(ctx)
	if err != nil {
		return err
	}
	defer done(ctx)

	return proxy.NewTransferrer(c.conn, c.streamCreator()).Transfer(ctx, src, dest, opts...)
}

func (c *Client) streamCreator() streaming.StreamCreator {
	return streamproxy.NewStreamCreator(c.conn)
}
