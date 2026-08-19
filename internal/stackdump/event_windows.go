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
	"fmt"
	"os"
	"strconv"
	"unsafe"

	"github.com/containerd/log"
	"golang.org/x/sys/windows"
)

// eventSecurityDescriptor grants full access to builtin administrators and local
// system only, on a DACL protected from inheritance. Signaling the event makes a
// privileged process dump its internals to disk, so an unprivileged one must not
// be able to reach it.
const eventSecurityDescriptor = "D:P(A;;GA;;;BA)(A;;GA;;;SY)"

// EventName returns the name of the Win32 event that triggers a stack dump in
// the process with the given pid.
//
// The daemon and every shim use this same scheme, so one tool can trigger a dump
// in any of them. PIDs are unique machine-wide, so the pid alone is enough to
// keep two processes from contending for a name.
func EventName(pid int) string {
	return `Global\stackdump-` + strconv.Itoa(pid)
}

// Notify calls fn every time this process's stack dump event is signaled, for
// example from PowerShell:
//
//	[System.Threading.EventWaitHandle]::OpenExisting("Global\stackdump-$targetPid").Set()
//
// Substitute the target's pid for $targetPid. PowerShell's own $PID is an
// automatic variable holding the pid of the session, not of the process to dump.
//
// fn runs on a dedicated goroutine that lives for the life of the process, so a
// slow fn delays later requests but nothing else.
//
// Callers should report an error and carry on: losing the debug facility is not a
// reason to fail startup.
func Notify(fn func()) error {
	return notify(EventName(os.Getpid()), fn)
}

// notify is the body of Notify with the event name supplied explicitly, so
// tests can use a name that does not collide with the running process's own.
func notify(event string, fn func()) error {
	ev, err := windows.UTF16PtrFromString(event)
	if err != nil {
		return fmt.Errorf("encoding event name %s: %w", event, err)
	}
	sd, err := windows.SecurityDescriptorFromString(eventSecurityDescriptor)
	if err != nil {
		return fmt.Errorf("building security descriptor for event %s: %w", event, err)
	}
	var sa windows.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	// Deliberately not inheritable. An inherited handle carries the access
	// rights it was created with, so a child process could signal dumps
	// without the DACL above ever being consulted again.
	sa.InheritHandle = 0
	sa.SecurityDescriptor = sd

	// Auto-reset and initially non-signaled: each signaling wakes exactly one
	// dump, and no dump is queued before anyone asks for one.
	h, err := windows.CreateEvent(&sa, 0, 0, ev)
	if h == 0 || err != nil {
		return fmt.Errorf("creating event %s: %w", event, err)
	}

	go func() {
		log.L.WithField("event", event).Debug("waiting for stackdump signal")
		for {
			windows.WaitForSingleObject(h, windows.INFINITE)
			fn()
		}
	}()
	return nil
}
