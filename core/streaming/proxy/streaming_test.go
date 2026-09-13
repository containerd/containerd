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

package proxy

import (
	"io"
	"testing"

	"github.com/containerd/typeurl/v2"
	"google.golang.org/protobuf/types/known/anypb"
)

// unsafeStreamClient writes a flag in CloseSend that Send reads, as ttrpc does.
type unsafeStreamClient struct {
	closed bool
}

func (c *unsafeStreamClient) Send(*anypb.Any) error {
	if c.closed {
		return io.EOF
	}
	return nil
}

func (c *unsafeStreamClient) Recv() (*anypb.Any, error) { return &anypb.Any{}, nil }
func (c *unsafeStreamClient) CloseSend() error          { c.closed = true; return nil }
func (c *unsafeStreamClient) SendMsg(m any) error       { return nil }
func (c *unsafeStreamClient) RecvMsg(m any) error       { return nil }

// TestClientStreamSendClose races under -race unless Send and Close are serialized.
func TestClientStreamSendClose(t *testing.T) {
	cs := &clientStream{s: &unsafeStreamClient{}}
	var a typeurl.Any = &anypb.Any{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			cs.Send(a)
		}
	}()

	if err := cs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-done
}
