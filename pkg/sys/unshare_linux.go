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
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	unprivilegedUsernsSupported     bool
	unprivilegedUsernsSupportedOnce sync.Once
)

// SupportsUnprivilegedUsernsCreation returns true if creating user namespaces
// as an unprivileged user is supported
func SupportsUnprivilegedUsernsCreation() bool {
	unprivilegedUsernsSupportedOnce.Do(func() {
		if err := checkUnprivilegedUsernsCreation(); err != nil {
			unprivilegedUsernsSupported = false
			return
		}
		unprivilegedUsernsSupported = true
	})
	return unprivilegedUsernsSupported
}

// checkUnprivilegedUsernsCreation tests if we can create a user namespace
// as an unprivileged user. This can fail on systems that deny unprivileged
// user namespaces through various means like AppArmor
func checkUnprivilegedUsernsCreation() error {
	// Assume nobody user is unprivileged
	nobodyUID := 65534
	var pidfd int

	_, pidfd, err := startProcessWithUserNamespace(nobodyUID, syscall.CLONE_NEWIPC)
	if err != nil {
		return fmt.Errorf("user namespace creation as unprivileged user failed: %w", err)
	}

	// Clean up the test process
	if pidfd != -1 {
		unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
		pidfdWaitid(pidfd)
		unix.Close(pidfd)
	}

	return nil
}

// UnshareAfterEnterUserns allows to disassociate parts of its execution context
// within a user namespace.
func UnshareAfterEnterUserns(uidMap, gidMap string, unshareFlags uintptr, f func(pid int) error) (retErr error) {
	if unshareFlags&syscall.CLONE_NEWUSER == syscall.CLONE_NEWUSER {
		return fmt.Errorf("unshare flags should not include user namespace")
	}

	if !SupportsPidFD() {
		return fmt.Errorf("kernel doesn't support pidfd")
	}

	uidMaps, err := parseIDMapping(uidMap)
	if err != nil {
		return err
	}

	gidMaps, err := parseIDMapping(gidMap)
	if err != nil {
		return err
	}

	targetUID := os.Geteuid()
	if SupportsUnprivilegedUsernsCreation() {
		targetUID = uidMaps[0].HostID
	}

	pid, pidfd, err := startProcessWithUserNamespace(targetUID, unshareFlags)
	if err != nil {
		return err
	}

	defer unix.Close(pidfd)
	defer func() {
		derr := unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
		if derr != nil {
			if !errors.Is(derr, unix.ESRCH) {
				retErr = derr
			}
			return
		}
		pidfdWaitid(pidfd)
	}()

	// Now we can write the uid/gid mappings
	uidMapPath := fmt.Sprintf("/proc/%d/uid_map", pid)
	uidMapContent := fmt.Sprintf("%d %d %d\n", uidMaps[0].ContainerID, uidMaps[0].HostID, uidMaps[0].Size)
	if err := os.WriteFile(uidMapPath, []byte(uidMapContent), 0644); err != nil {
		return fmt.Errorf("failed to write UID mapping: %w", err)
	}

	gidMapPath := fmt.Sprintf("/proc/%d/gid_map", pid)
	gidMapContent := fmt.Sprintf("%d %d %d\n", gidMaps[0].ContainerID, gidMaps[0].HostID, gidMaps[0].Size)
	if err := os.WriteFile(gidMapPath, []byte(gidMapContent), 0644); err != nil {
		return fmt.Errorf("failed to write GID mapping: %w", err)
	}

	if f != nil {
		if err := f(pid); err != nil {
			return err
		}
	}

	// Ensure the child process is still alive. If the err is ESRCH, we
	// should return error because the pid could be reused. It's safe to
	// return error and retry.
	if err := unix.PidfdSendSignal(pidfd, 0, nil, 0); err != nil {
		return fmt.Errorf("failed to ensure child process is alive: %w", err)
	}
	return nil
}

// TODO: Support multiple mappings in future
func parseIDMapping(mapping string) ([]syscall.SysProcIDMap, error) {
	parts := strings.Split(mapping, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("user namespace mappings require the format `container-id:host-id:size`")
	}

	cID, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid container id for user namespace remapping, %w", err)
	}

	hID, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid host id for user namespace remapping, %w", err)
	}

	size, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid size for user namespace remapping, %w", err)
	}

	if cID < 0 || hID < 0 || size < 0 {
		return nil, fmt.Errorf("invalid mapping %s, all IDs and size must be positive integers", mapping)
	}

	return []syscall.SysProcIDMap{
		{
			ContainerID: cID,
			HostID:      hID,
			Size:        size,
		},
	}, nil
}

// startProcessWithUserNamespace starts a ptraced dummy process to create
// a user namespace. It runs the thread as targetHostUID when creating the process
// and user namespace, ensuring that the kernel attributes user limits to targetHostUID
// and not containerd's user. On success it the pid, pidfd and no error.
// It is expected that the caller sets up the uid_map/gid_map for the
// pid (this cannot be done as part of os.StartProcess() since targetHostUID doesn't
// have CAP_SETUID)
func startProcessWithUserNamespace(targetHostUID int, unshareFlags uintptr) (int, int, error) {
	var pidfd int
	originalEUID := os.Geteuid()

	// Lock the thread, and use setresuid() directly to only apply the effective uid to this thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if _, _, errno := syscall.RawSyscall(unix.SYS_SETRESUID, ^uintptr(0), uintptr(targetHostUID), ^uintptr(0)); errno != 0 {
		return -1, -1, fmt.Errorf("failed to set effective UID: %w", errno)
	}
	defer func() {
		syscall.RawSyscall(unix.SYS_SETRESUID, ^uintptr(0), uintptr(originalEUID), ^uintptr(0))
	}()

	proc, err := os.StartProcess("/proc/self/exe", []string{"UnshareAfterEnterUserns"}, &os.ProcAttr{
		Sys: &syscall.SysProcAttr{
			// clone new user namespace first and then unshare
			Cloneflags:   unix.CLONE_NEWUSER,
			Unshareflags: unshareFlags,
			// NOTE: It's reexec but it's not heavy because subprocess
			// be in PTRACE_TRACEME mode before performing execve.
			Ptrace:    true,
			Pdeathsig: syscall.SIGKILL,
			PidFD:     &pidfd,
		},
	})

	if err != nil {
		return -1, -1, fmt.Errorf("failed to start noop process for unshare: %w", err)
	}

	if pidfd == -1 {
		proc.Kill()
		proc.Wait()
		return -1, -1, fmt.Errorf("kernel doesn't support CLONE_PIDFD")
	}

	return proc.Pid, pidfd, nil
}

func pidfdWaitid(pidfd int) error {
	return IgnoringEINTR(func() error {
		return unix.Waitid(unix.P_PIDFD, pidfd, nil, unix.WEXITED, nil)
	})
}
