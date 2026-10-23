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

// containerd-sandbox-init is PID 1 of the PID namespace of a pod that shares
// it (shareProcessNamespace) and runs under containerd-shim-sandboxed-runc-v2,
// the pause-less sandbox shim. The shim runs it through runc for those pods
// only; an ordinary pod has no sandbox process at all.
//
// It does what PID 1 of a pod has to do and nothing else: it stays alive so
// that the namespace does, it lets the kernel reap the orphans that get
// reparented to it, and it cannot be ended from inside the pod. The kernel
// delivers a signal to the init of a PID namespace, from inside that
// namespace, only when init has a handler installed for it, and discards it
// otherwise. SIGKILL and SIGSTOP reach it from the parent namespace only,
// and SIGKILL from the shim is how it ends. The Go runtime installs handlers
// for almost every signal and ends the process on most of them, so a Go init
// that left them in place could be killed from a member container with
// kill -HUP 1 or kill -SEGV 1; every handler is removed here.
package main

import (
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func main() {
	run()
}

// run sets the process up and parks it; it never returns.
func run() {
	// One P and no garbage collector: there is nothing to run and nothing
	// to collect, and this keeps the runtime from waking up.
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(-1)
	if err := disableSignals(); err != nil {
		os.Exit(1)
	}
	debug.FreeOSMemory()
	for {
		// A blocking system call rather than select {}: with no other
		// goroutine the runtime would report a deadlock.
		unix.Pause()
	}
}

// disableSignals leaves no signal handler in place. SIGCHLD is ignored
// rather than defaulted so that the kernel reaps the children reparented to
// PID 1 on its own, with no wait loop. os/signal cannot touch the synchronous
// faults, for which the runtime installs handlers that exit the process when
// the signal came from kill(2); those go back to the kernel default through
// rt_sigaction.
func disableSignals() error {
	for n := 1; n <= 64; n++ {
		sig := syscall.Signal(n)
		switch sig {
		case unix.SIGKILL, unix.SIGSTOP:
			// Uncatchable, and how the shim ends the process.
		case unix.SIGURG, unix.SIGPROF:
			// The runtime keeps its preemption and profiling handlers,
			// which do nothing on a signal sent by a process.
		case unix.SIGILL, unix.SIGTRAP, unix.SIGBUS, unix.SIGFPE, unix.SIGSEGV, unix.SIGSTKFLT, unix.SIGSYS:
			if err := defaultSignal(sig); err != nil {
				return err
			}
		default:
			signal.Ignore(sig)
		}
	}
	return nil
}

// defaultSignal restores the kernel default disposition of sig. The action
// passed is a zeroed struct sigaction, SIG_DFL with no flags and an empty
// mask, sized for every architecture (the struct differs only in whether it
// has a restorer field); the last argument is the size of the kernel sigset.
func defaultSignal(sig syscall.Signal) error {
	var act [4]uint64
	if _, _, errno := unix.RawSyscall6(unix.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), 0, 8, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
