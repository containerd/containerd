//go:build linux

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
	"os"
	"testing"

	"github.com/containerd/fifo"
)

func TestCheckCopyShimLogError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	if err := checkCopyShimLogError(ctx, fifo.ErrReadClosed); err != fifo.ErrReadClosed {
		t.Fatalf("should return the actual error before context is done, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, nil); err != nil {
		t.Fatalf("should return the actual error before context is done, but %v", err)
	}

	cancel()

	if err := checkCopyShimLogError(ctx, fifo.ErrReadClosed); err != nil {
		t.Fatalf("should return nil when error is ErrReadClosed after context is done, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, nil); err != nil {
		t.Fatalf("should return the actual error after context is done, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, os.ErrClosed); err != nil {
		t.Fatalf("should return the actual error after context is done, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, fifo.ErrRdFrmWRONLY); err != fifo.ErrRdFrmWRONLY {
		t.Fatalf("should return the actual error after context is done, but %v", err)
	}
}
