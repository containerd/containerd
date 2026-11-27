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
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/typeurl/v2"
)

// scriptedStream replays a fixed list of window updates to the reader goroutine and
// records the payload the writer sends back, standing in for a hostile transfer peer.
type scriptedStream struct {
	updates []int32
	pos     int

	mu   sync.Mutex
	sent []byte
}

func (s *scriptedStream) Recv() (typeurl.Any, error) {
	if s.pos >= len(s.updates) {
		return nil, io.EOF
	}
	u := s.updates[s.pos]
	s.pos++
	return typeurl.MarshalAny(&transferapi.WindowUpdate{Update: u})
}

func (s *scriptedStream) Send(a typeurl.Any) error {
	v, err := typeurl.UnmarshalAny(a)
	if err != nil {
		return err
	}
	if d, ok := v.(*transferapi.Data); ok {
		s.mu.Lock()
		s.sent = append(s.sent, d.Data...)
		s.mu.Unlock()
	}
	return nil
}

func (s *scriptedStream) Close() error { return nil }

func (s *scriptedStream) received() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.sent...)
}

// countingReader counts zero-length Read calls. SendStream reads into a slice sized by the
// window; it would ask for zero bytes only if it had accepted a non-positive credit, so a
// non-zero count means the <= 0 rejection regressed.
type countingReader struct {
	r         io.Reader
	zeroReads atomic.Int32
}

func (c *countingReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		c.zeroReads.Add(1)
	}
	return c.r.Read(p)
}

// TestWriteByteStreamAddWindowPreservesCredit pins the int64 accumulator: two max credits
// add up to 2*MaxInt32 instead of overflowing an int32 counter back to a negative value
// that Write would turn into a negative slice bound. It reads the counter directly so the
// check does not race the Write goroutine.
func TestWriteByteStreamAddWindowPreservesCredit(t *testing.T) {
	wbs := &writeByteStream{}
	wbs.addWindow(math.MaxInt32)
	wbs.addWindow(math.MaxInt32)
	if got, want := wbs.remaining.Load(), int64(math.MaxInt32)*2; got != want {
		t.Fatalf("remaining = %d, want %d (int64 credit preserved, not overflowed)", got, want)
	}
}

// TestWriteByteStreamIgnoresInvalidThenRecovers sends an invalid update, then a valid
// credit, and asserts the writer drops the first, accepts the second, and delivers the
// whole payload. It fails with a panic on the pre-fix code and deterministically proves
// recovery afterwards; the timeout is only a failsafe, not the success condition.
func TestWriteByteStreamIgnoresInvalidThenRecovers(t *testing.T) {
	const payload = "transfer-byte-stream-payload"
	for _, tc := range []struct {
		name    string
		updates []int32
	}{
		{"negative then valid credit", []int32{-1, int32(len(payload))}},
		{"zero then valid credit", []int32{0, int32(len(payload))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &scriptedStream{updates: tc.updates}
			w := WriteByteStream(t.Context(), peer)

			done := make(chan error, 1)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						done <- fmt.Errorf("Write panicked: %v", r)
					}
				}()
				_, err := w.Write([]byte(payload))
				done <- err
			}()

			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Write did not return; the timeout is a failsafe, not a pass")
			}
			if got := string(peer.received()); got != payload {
				t.Fatalf("peer received %q, want %q", got, payload)
			}
		})
	}
}

// TestSendStreamIgnoresInvalidThenRecovers is the SendStream counterpart: an invalid update
// followed by a valid credit must still deliver the payload from the background goroutine,
// and the invalid credit must not have driven a zero-length read. The pre-fix code takes
// the process down with a panic on the invalid update.
func TestSendStreamIgnoresInvalidThenRecovers(t *testing.T) {
	const payload = "transfer-send-stream-payload"
	for _, tc := range []struct {
		name    string
		updates []int32
	}{
		{"negative then valid credit", []int32{-1, int32(len(payload))}},
		{"zero then valid credit", []int32{0, int32(len(payload))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &scriptedStream{updates: tc.updates}
			cr := &countingReader{r: strings.NewReader(payload)}
			SendStream(t.Context(), cr, peer)
			waitForPayload(t, peer, payload)
			if z := cr.zeroReads.Load(); z != 0 {
				t.Fatalf("SendStream made %d zero-length reads; a non-positive credit was accepted", z)
			}
		})
	}
}

// TestSendStreamLargeCreditDelivers feeds two max credits that would overflow an int32
// counter back to negative; the int64 budget accumulates them and delivers the whole
// payload without a background panic.
func TestSendStreamLargeCreditDelivers(t *testing.T) {
	payload := strings.Repeat("a", 3*maxRead)
	peer := &scriptedStream{updates: []int32{math.MaxInt32, math.MaxInt32}}
	SendStream(t.Context(), strings.NewReader(payload), peer)
	waitForPayload(t, peer, payload)
}

// waitForPayload polls until the peer has received the full payload, failing on a
// timeout used only as a failsafe against a hang.
func waitForPayload(t *testing.T, peer *scriptedStream, payload string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if string(peer.received()) == payload {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("peer received %d bytes, want %d", len(peer.received()), len(payload))
		case <-time.After(5 * time.Millisecond):
		}
	}
}
