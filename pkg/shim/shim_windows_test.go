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

package shim

import (
	"strings"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
)

// TestAwaitPipeReady_State1_RetriesOnErrTimeout proves that awaitPipeReady
// retries when winio.DialPipe returns winio.ErrTimeout (pipe in state 1:
// ListenPipe called, Accept not yet called).
//
// With the pre-fix code this test fails: awaitPipeReady returns winio.ErrTimeout
// immediately after the first dial attempt instead of retrying.
//
// After the fix (errors.Is(err, winio.ErrTimeout) treated as retryable) the
// test passes: the function retries past the timeout, Accept fires at 1.5 s,
// and the next dial attempt connects.
func TestAwaitPipeReady_State1_RetriesOnErrTimeout(t *testing.T) {
	pipePath := `\\.\pipe\` + strings.ReplaceAll(t.Name(), "/", "_")

	// Create the pipe listener (state 1): ListenPipe called, Accept not yet
	// called. winio.DialPipe against a state-1 pipe blocks for the per-attempt
	// timeout then returns winio.ErrTimeout — not context.DeadlineExceeded.
	listener, err := winio.ListenPipe(pipePath, nil)
	if err != nil {
		t.Fatalf("ListenPipe: %v", err)
	}
	defer listener.Close()

	// Transition to state 2 after 1.5 s — intentionally longer than the 1-second
	// per-attempt DialPipe timeout so the first attempt always returns ErrTimeout.
	// awaitPipeReady's 5-second overall timer still leaves enough room for a
	// successful retry once Accept fires.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	start := time.Now()
	if err := awaitPipeReady(pipePath); err != nil {
		t.Fatalf("awaitPipeReady(%q) = %v; want nil", pipePath, err)
	}
	// Assert at least one per-attempt timeout (1s) elapsed, proving the
	// retry path was exercised and the test is not a false positive from
	// Accept firing before the first dial attempt completed.
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("awaitPipeReady returned in %v; want ≥1s to confirm retry path was taken", elapsed)
	}
}
