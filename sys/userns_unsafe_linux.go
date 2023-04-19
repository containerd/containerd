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

package sys

import (
	"runtime"
	"syscall"
	"unsafe"
)

// ProcSyncType is used for synchronization
// between parent and child processes.
type ProcSyncType uint8

const (
	// ProcSyncExit tells child "it's time to exit".
	ProcSyncExit ProcSyncType = 0x1
)

//go:norace
//go:noinline
func ForkUserns(pipeMap [2]int) (pid uintptr, errno syscall.Errno) {
	var sync ProcSyncType

	beforeFork()
	if runtime.GOARCH == "s390x" {
		pid, _, errno = syscall.RawSyscall6(uintptr(syscall.SYS_CLONE), 0, syscall.CLONE_NEWUSER|uintptr(syscall.SIGCHLD), 0, 0, 0, 0)
	} else {
		pid, _, errno = syscall.RawSyscall6(uintptr(syscall.SYS_CLONE), syscall.CLONE_NEWUSER|uintptr(syscall.SIGCHLD), 0, 0, 0, 0, 0)
	}
	if errno != 0 || pid != 0 {
		afterFork()
		return pid, errno
	}

	afterForkInChild()
	if _, _, errno = syscall.RawSyscall(syscall.SYS_CLOSE, uintptr(pipeMap[1]), 0, 0); errno != 0 {
		goto err
	}
	if _, _, errno = syscall.RawSyscall6(syscall.SYS_PRCTL, syscall.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0, 0); errno != 0 {
		goto err
	}
	// wait for parent's signal
	if _, _, errno = syscall.RawSyscall6(syscall.SYS_READ, uintptr(pipeMap[0]), uintptr(unsafe.Pointer(&sync)), unsafe.Sizeof(sync), 0, 0, 0); errno != 0 || sync != ProcSyncExit {
		goto err
	}

err:
	syscall.RawSyscall6(syscall.SYS_EXIT, uintptr(errno), 0, 0, 0, 0, 0)
	panic("unreachable")
}
