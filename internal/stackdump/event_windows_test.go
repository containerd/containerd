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

package stackdump

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// Long enough to survive a stalled CI runner: it only elapses when the test
	// is already failing, so generosity here costs nothing.
	waitTimeout   = 10 * time.Second
	retryInterval = 50 * time.Millisecond
)

// eventCounter keeps event names unique across tests in this package.
var eventCounter atomic.Uint64

func TestEventName(t *testing.T) {
	// External tooling signals this name to trigger a dump, and the containerd
	// daemon and its shims must agree on it, so it is a compatibility contract.
	if got, want := EventName(1234), `Global\stackdump-1234`; got != want {
		t.Fatalf("EventName(1234) = %q; want %q", got, want)
	}
}

// startNotify registers a watcher on a uniquely named event and returns a
// handle for signaling it.
//
// Creating an event in the Global\ namespace needs SeCreateGlobalPrivilege and
// the event's DACL admits only builtin administrators and local system, so an
// unelevated run cannot exercise this. That is the behavior under test, not a
// failure of it, so skip rather than fail.
func startNotify(t *testing.T, fn func()) windows.Handle {
	t.Helper()
	name := fmt.Sprintf(`Global\stackdump-test-%d-%d`, os.Getpid(), eventCounter.Add(1))
	if err := notify(name, fn); err != nil {
		if !isElevationError(err) {
			t.Fatalf("notify(%s) error = %v", name, err)
		}
		t.Skipf("cannot create stackdump event %s, test requires elevation: %v", name, err)
	}
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatalf("failed to encode event name %s: %v", name, err)
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE|windows.SYNCHRONIZE, false, n)
	if err != nil {
		if !isElevationError(err) {
			t.Fatalf("failed to open stackdump event %s: %v", name, err)
		}
		t.Skipf("cannot open stackdump event %s, test requires elevation: %v", name, err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	return h
}

// isElevationError reports whether err is the failure an unelevated run
// produces. Skipping on anything else would hide the regressions these tests
// exist to catch: ERROR_FILE_NOT_FOUND, for one, means no event was created.
func isElevationError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}

func TestNotifyRunsCallbackPerSignal(t *testing.T) {
	calls := make(chan struct{}, 8)
	h := startNotify(t, func() { calls <- struct{}{} })

	// The event is auto-reset, so each signaling must produce exactly one call.
	for i := range 3 {
		if err := windows.SetEvent(h); err != nil {
			t.Fatalf("SetEvent() error = %v", err)
		}
		select {
		case <-calls:
		case <-time.After(waitTimeout):
			t.Fatalf("callback was not run after signaling the event (iteration %d)", i)
		}
		select {
		case <-calls:
			t.Fatalf("callback ran more than once for a single signaling (iteration %d)", i)
		case <-time.After(retryInterval):
		}
	}
}

// TestNotifyKeepsServingAfterSlowCallback covers the containerd daemon's usage,
// where the callback writes a stack dump to disk before returning: a slow
// callback must delay later requests, not end them.
func TestNotifyKeepsServingAfterSlowCallback(t *testing.T) {
	calls := make(chan struct{}, 8)
	var first atomic.Bool
	h := startNotify(t, func() {
		if first.CompareAndSwap(false, true) {
			time.Sleep(2 * retryInterval)
		}
		calls <- struct{}{}
	})

	if err := windows.SetEvent(h); err != nil {
		t.Fatalf("SetEvent() error = %v", err)
	}
	select {
	case <-calls:
	case <-time.After(waitTimeout):
		t.Fatal("callback was not run after signaling the event")
	}

	if err := windows.SetEvent(h); err != nil {
		t.Fatalf("SetEvent() error = %v", err)
	}
	select {
	case <-calls:
	case <-time.After(waitTimeout):
		t.Fatal("watcher stopped serving requests after a slow callback")
	}
}

func TestNotifyRejectsInvalidEventName(t *testing.T) {
	// A NUL byte cannot be encoded into a UTF-16 name, so notify must report
	// the failure rather than start a watcher on nothing.
	if err := notify("Global\\stackdump\x00bad", func() {}); err == nil {
		t.Fatal("notify() with an invalid event name = nil; want an error")
	}
}
