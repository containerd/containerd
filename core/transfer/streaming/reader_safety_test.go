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

package streaming

import (
	"bytes"
	"io"
	"testing"
)

// Characterization test for ReadByteStream's flow-control window.
//
// This test must PASS both before and after the fix in normal mode (the
// observable contract: every byte sent is received intact). Under `-race` it
// FAILS on the unpatched code and PASSES after the fix -- that failure is the
// reproduction of the P1-7 data race.
//
// Background:
//
//	ReadByteStream spawns a background goroutine (reader.go) that advertises
//	flow-control credit and mutates `window` (`window += windowSize`), while the
//	consumer calls Read in a *different* goroutine, which also mutates `window`
//	(`window = window - int32(n)`) and reads it. `window` is a plain int32 with
//	no synchronization, so the two goroutines race on it. The sibling
//	writeByteStream uses atomic.Int32 for the symmetric field, confirming the
//	reader's plain int32 is an oversight.
//
// Faithful, recommended-usage reproduction (no artificial goroutines):
//   - Pairs the real exported ReadByteStream (consumer under test) with the real
//     exported SendStream (the remote producer) over the package's own
//     testStream seam -- exactly the production topology (a remote sender feeds
//     Data in response to the window updates ReadByteStream advertises).
//   - Consumes via io.Copy, the same way the production consumer drains it
//     (internal/cri/io.ContainerIO.Pipe: io.Copy(stdoutGroup, stdout)); io.Copy's
//     32KiB buffer matches maxRead.
//   - The racing background goroutine is the one ReadByteStream spawns itself; it
//     runs concurrently with Read by construction, not by test contrivance.
//   - A multi-MB payload drives many advertise/consume cycles so the two
//     goroutines touch `window` concurrently many times within one stream.
func TestReadByteStreamSafety_WindowNoRace(t *testing.T) {
	ctx := t.Context()

	// windowSize is 64KiB; a few MB yields many window-advertise / window-consume
	// cycles, maximizing the chance the advertise goroutine's `window += windowSize`
	// overlaps Read's `window -= n`.
	expected := bytes.Repeat([]byte("containerd-streaming-window-race-probe!"), 120_000)

	rs, ws := pipeStream()
	SendStream(ctx, bytes.NewReader(expected), ws)
	rbs := ReadByteStream(ctx, rs)

	var actual bytes.Buffer
	n, err := io.Copy(&actual, rbs)
	if err != nil {
		t.Fatalf("io.Copy from ReadByteStream returned error: %v", err)
	}
	if int(n) != len(expected) {
		t.Fatalf("received %d bytes, expected %d (stream truncated)", n, len(expected))
	}
	if !bytes.Equal(expected, actual.Bytes()) {
		t.Fatal("received bytes differ from sent bytes (stream corrupted)")
	}
}
