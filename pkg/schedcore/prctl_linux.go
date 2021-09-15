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

package schedcore

import (
	"golang.org/x/sys/unix"
)

// PidType is the type of provided pid value and how it should be treated
type PidType int

const (
	// Pid affects the current pid
	Pid PidType = pidtypePid
	// ThreadGroup affects all threads in the group
	ThreadGroup PidType = pidtypeTgid
	// ProcessGroup affects all processes in the group
	ProcessGroup PidType = pidtypePgid
)

const (
	pidtypePid  = 0
	pidtypeTgid = 1
	pidtypePgid = 2
)

// Create a new sched core domain
func Create(t PidType) error {
	return unix.Prctl(unix.PR_SCHED_CORE, unix.PR_SCHED_CORE_CREATE, 0, uintptr(t), 0)
}

// ShareFrom shares the sched core domain from the provided pid
func ShareFrom(pid uint64, t PidType) error {
	return unix.Prctl(unix.PR_SCHED_CORE, unix.PR_SCHED_CORE_SHARE_FROM, uintptr(pid), uintptr(t), 0)
}
