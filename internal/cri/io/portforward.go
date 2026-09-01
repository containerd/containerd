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

package io

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// portForwardIO presents the "-in" and "-out" streams of a port forward as a single
// duplex pipe.
type portForwardIO struct {
	in  io.WriteCloser
	out io.ReadCloser
}

// NewStreamPortForwardIO opens the "<streamID>-in" and "<streamID>-out" streams on the
// sandbox streaming endpoint at address and returns them as a duplex pipe. The sandbox
// must already be serving both; see the sandbox PortForward RPC.
func NewStreamPortForwardIO(ctx context.Context, address, streamID string) (io.ReadWriteCloser, error) {
	in, err := openStdinStream(ctx, fmt.Sprintf("%s?streaming_id=%s-in", address, streamID))
	if err != nil {
		return nil, fmt.Errorf("failed to open port forward input stream: %w", err)
	}
	out, err := openOutputStream(ctx, fmt.Sprintf("%s?streaming_id=%s-out", address, streamID))
	if err != nil {
		in.Close()
		return nil, fmt.Errorf("failed to open port forward output stream: %w", err)
	}
	return &portForwardIO{in: in, out: out}, nil
}

func (p *portForwardIO) Read(b []byte) (int, error) {
	return p.out.Read(b)
}

func (p *portForwardIO) Write(b []byte) (int, error) {
	return p.in.Write(b)
}

func (p *portForwardIO) Close() error {
	return errors.Join(p.in.Close(), p.out.Close())
}
