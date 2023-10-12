//go:build !windows && !linux

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

	"github.com/containerd/containerd/errdefs"
)

// portForward uses netns to enter the sandbox namespace, and forwards a stream inside the
// namespace to a specific port. It keeps forwarding until it exits or client disconnect.
func (c *criService) portForward(ctx context.Context, id string, port int32, stream io.ReadWriteCloser) error {
	return fmt.Errorf("port forward: %w", errdefs.ErrNotImplemented)
}
