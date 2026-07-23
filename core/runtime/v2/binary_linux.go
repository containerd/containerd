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

package v2

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func copyShimlog(_ context.Context, path string, _ io.ReadCloser, done chan struct{}) error {
	fifo, err := os.OpenFile(path, os.O_RDONLY, 0700)
	if err != nil {
		return err
	}
	defer fifo.Close()
	for {
		select {
		case <-done:
			return nil
		default:
			n, err := unix.Splice(
				int(fifo.Fd()),
				nil,
				int(os.Stderr.Fd()),
				nil,
				1024*4,
				0,
			)
			if err != nil {
				if err == unix.EAGAIN {
					continue
				}
				if errors.Is(err, unix.EPIPE) || errors.Is(err, os.ErrClosed) {
					return nil
				}
				return err
			}
			if n == 0 {
				continue
			}
		}
	}
}
