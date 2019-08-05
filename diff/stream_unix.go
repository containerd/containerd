// +build !windows

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

package diff

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
)

// NewBinaryProcessor returns a binary processor for use with processing content streams
func NewBinaryProcessor(ctx context.Context, imt, rmt string, stream StreamProcessor, name string, args []string, payload *types.Any) (StreamProcessor, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var payloadC io.Closer
	if payload != nil {
		data, err := proto.Marshal(payload)
		if err != nil {
			return nil, err
		}
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		go func() {
			io.Copy(w, bytes.NewReader(data))
			w.Close()
		}()

		cmd.ExtraFiles = append(cmd.ExtraFiles, r)
		payloadC = r
	}
	cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", mediaTypeEnvVar, imt))
	var (
		stdin  io.Reader
		closer func() error
		err    error
	)
	if f, ok := stream.(RawProcessor); ok {
		stdin = f.File()
		closer = f.File().Close
	} else {
		stdin = stream
	}
	cmd.Stdin = stdin
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = w

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go cmd.Wait()

	// close after start and dup
	w.Close()
	if closer != nil {
		closer()
	}
	if payloadC != nil {
		payloadC.Close()
	}
	return &binaryProcessor{
		cmd: cmd,
		r:   r,
		mt:  rmt,
	}, nil
}

type binaryProcessor struct {
	cmd *exec.Cmd
	r   *os.File
	mt  string
}

func (c *binaryProcessor) File() *os.File {
	return c.r
}

func (c *binaryProcessor) MediaType() string {
	return c.mt
}

func (c *binaryProcessor) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func (c *binaryProcessor) Close() error {
	err := c.r.Close()
	if kerr := c.cmd.Process.Kill(); err == nil {
		err = kerr
	}
	return err
}
