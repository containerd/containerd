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
// it (shareProcessNamespace). containerd-shim-sandboxed-runc-v2, the
// pause-less sandbox shim, starts it through runc for those pods only, and it
// runs as a child of the shim. An ordinary pod, one that does not share its
// PID namespace, has no sandbox process at all.
//
// It does what PID 1 of a pod has to do and nothing else: it stays alive so
// that the namespace does, it lets the kernel reap the orphans that get
// reparented to it, and it cannot be ended from inside the pod.
//
// The kernel delivers a signal to the init of a PID namespace, from inside
// that namespace, only when init has a handler installed for it, and discards
// it otherwise. SIGKILL and SIGSTOP reach it from the parent namespace only,
// and SIGKILL from the shim is how it ends. The Go runtime installs handlers
// for almost every signal and ends the process on most of them. A Go init
// that kept those handlers could be killed from a member container with
// kill -HUP 1 or kill -SEGV 1, so disableSignals removes every one of them.
//
// It is also not dumpable, so that the other processes of the pod cannot
// inspect or control it: without CAP_SYS_PTRACE they can neither ptrace it
// nor open /proc/1/root, /proc/1/mem and the like.
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
	// runc makes the process non-dumpable, but execve made it dumpable
	// again, since it changed no credentials.
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		os.Exit(1)
	}
	// A single scheduler thread (GOMAXPROCS) and no garbage collector:
	// there is nothing to run and nothing to collect. Like pause, the
	// process then sleeps until it is killed.
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

// disableSignals leaves no signal handler in place, so that no signal sent
// from inside the pod runs any code in the process. SIGCHLD is ignored rather
// than defaulted so that the kernel reaps the children reparented to PID 1 on
// its own, with no wait loop. Ignoring SIGURG gives up asynchronous
// preemption, which a process whose only goroutine waits in pause(2) does not
// need. os/signal cannot touch the synchronous faults, for which the runtime
// installs handlers that exit the process when the signal came from kill(2),
// nor SIGPROF, whose handler it keeps for profiling, nor signals 32 and 33,
// which the C library reserves and the runtime keeps for itself (33 for
// system calls that must run on every thread, which this process never
// makes): the faults go back to the kernel default and the others are
// ignored, through rt_sigaction.
func disableSignals() error {
	// Signals 32 and 33, reserved by the C library and kept by the Go runtime.
	const (
		sigRuntime1 = syscall.Signal(32)
		sigRuntime2 = syscall.Signal(33)
	)
	for n := 1; n <= 64; n++ {
		sig := syscall.Signal(n)
		switch sig {
		case unix.SIGKILL, unix.SIGSTOP:
			// Uncatchable, and how the shim ends the process.
		case unix.SIGILL, unix.SIGTRAP, unix.SIGBUS, unix.SIGFPE, unix.SIGSEGV, unix.SIGSTKFLT, unix.SIGSYS:
			if err := setAction(sig, sigDFL); err != nil {
				return err
			}
		case unix.SIGPROF, sigRuntime1, sigRuntime2:
			if err := setAction(sig, sigIGN); err != nil {
				return err
			}
		default:
			signal.Ignore(sig)
		}
	}
	return nil
}

// The dispositions setAction sets.
const (
	sigDFL = 0
	sigIGN = 1
)

// setAction sets the disposition of sig, with no flags and an empty mask. The
// struct sigaction passed is sized for every architecture and starts with the
// handler, as it does on every supported one (MIPS puts the flags first and
// has a larger sigset); the last argument is the size of the kernel sigset.
func setAction(sig syscall.Signal, handler uintptr) error {
	act := [4]uint64{uint64(handler)}
	if _, _, errno := unix.RawSyscall6(unix.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), 0, 8, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
