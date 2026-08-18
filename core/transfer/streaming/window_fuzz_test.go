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
	"context"
	"io"
	"math"
	"testing"
	"time"

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/typeurl/v2"
)

// hostileStream replays an arbitrary list of window updates to the reader goroutine
// and drops whatever the writer sends. It stands in for a fully attacker-controlled
// transfer peer that may send any int32 credit in any order.
type hostileStream struct {
	updates []int32
	pos     int
}

func (s *hostileStream) Recv() (typeurl.Any, error) {
	if s.pos >= len(s.updates) {
		return nil, io.EOF
	}
	u := s.updates[s.pos]
	s.pos++
	return typeurl.MarshalAny(&transferapi.WindowUpdate{Update: u})
}
func (s *hostileStream) Send(typeurl.Any) error { return nil }
func (s *hostileStream) Close() error           { return nil }

// windowPalette biases fuzz/brute-force input toward the dangerous edges: negatives,
// zero, the maxRead boundary, and the int32 extremes that drive overflow.
var windowPalette = []int32{
	-1, 0, 1, 2, maxRead - 1, maxRead, maxRead + 1,
	math.MaxInt32, math.MaxInt32 - 1, math.MinInt32, math.MinInt32 + 1, -maxRead,
}

func updatesFromBytes(b []byte) []int32 {
	u := make([]int32, 0, len(b)+1)
	for _, x := range b {
		u = append(u, windowPalette[int(x)%len(windowPalette)])
	}
	return u
}

// driveWriteByteStream runs the real WriteByteStream against a hostile update
// sequence, bounded by a short context so a starved Write does not hang, and
// reports whether Write panicked. A negative slice bound would panic here.
func driveWriteByteStream(updates []int32, payload []byte) (panicked bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	w := WriteByteStream(ctx, &hostileStream{updates: updates})
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	_, _ = w.Write(payload)
	return
}

// driveSendStream runs the real SendStream against a hostile update sequence. Its
// panic would be in a background goroutine, so it cannot be recovered here: an
// unfixed overflow crashes the whole test binary, which is exactly the signal.
func driveSendStream(updates []int32, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	SendStream(ctx, bytes.NewReader(payload), &hostileStream{updates: updates})
	time.Sleep(3 * time.Millisecond) // let the background goroutines run and possibly panic
}

// TestOriginalGHSAPoCNoLongerPanics is the exact GHSA-63h3-p4hq-m7mq reproducer:
// a peer whose first control message is WindowUpdate{-1}. It panicked p[:-1] on the
// pre-fix code; it must return cleanly now.
func TestOriginalGHSAPoCNoLongerPanics(t *testing.T) {
	if driveWriteByteStream([]int32{-1}, []byte("layer-bytes")) {
		t.Fatal("WriteByteStream still panics on the original WindowUpdate{-1} PoC")
	}
}

// FuzzWindowUpdates lets the fuzzer explore arbitrary window-update sequences and
// payloads against both send paths. A panic (WriteByteStream) or a background crash
// (SendStream) is a finding.
func FuzzWindowUpdates(f *testing.F) {
	f.Add([]byte{0}, []byte("layer-bytes"))                                  // {-1}
	f.Add([]byte{7, 7}, []byte("payload"))                                   // {MaxInt32, MaxInt32}
	f.Add([]byte{7, 4, 7}, bytes.Repeat([]byte("a"), 3*maxRead))             // overflow after a read
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, bytes.Repeat([]byte{9}, 8)) // mixed
	f.Fuzz(func(t *testing.T, control, payload []byte) {
		updates := updatesFromBytes(control)
		if driveWriteByteStream(updates, payload) {
			t.Fatalf("WriteByteStream panicked on updates=%v", updates)
		}
		driveSendStream(updates, payload)
	})
}
