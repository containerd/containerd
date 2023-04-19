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

package mount

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/sys"
	"github.com/sirupsen/logrus"
)

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
	if cID != 0 || hID < 0 || size < 0 {
		return nil, fmt.Errorf("invalid mapping %s, all IDs and size must be positive integers (container ID of 0 is only supported)", mapping)
	}
	return []syscall.SysProcIDMap{
		{
			ContainerID: cID,
			HostID:      hID,
			Size:        size,
		},
	}, nil
}

// IDMapMount applies GID/UID shift according to gidmap/uidmap for target path
func IDMapMount(source, target string, usernsFd int) (err error) {
	var (
		attr unix.MountAttr
	)

	attr.Attr_set = unix.MOUNT_ATTR_IDMAP
	attr.Attr_clr = 0
	attr.Propagation = 0
	attr.Userns_fd = uint64(usernsFd)

	dFd, err := unix.OpenTree(-int(unix.EBADF), source, uint(unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_EMPTY_PATH))
	if err != nil {
		return fmt.Errorf("Unable to open tree for %s: %w", target, err)
	}

	defer unix.Close(dFd)
	if err = unix.MountSetattr(dFd, "", unix.AT_EMPTY_PATH, &attr); err != nil {
		return fmt.Errorf("Unable to shift GID/UID for %s: %w", target, err)
	}

	if err = unix.MoveMount(dFd, "", -int(unix.EBADF), target, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
		return fmt.Errorf("Unable to attach mount tree to %s: %w", target, err)
	}
	return nil
}

// GetUsernsFD forks the current process and creates a user namespace using the specified
// mappings.
//
// It returns:
//  1. The file descriptor of the /proc/[pid]/ns/user of the newly
//     created mapping.
//  2. "Clean up" function that should be called once user namespace
//     file descriptor is no longer needed.
//  3. Usual error.
func GetUsernsFD(uidmap, gidmap string) (_ int, _ func(), err error) {
	var (
		usernsFile       *os.File
		pipeMap          [2]int
		pid              uintptr
		errno            syscall.Errno
		uidMaps, gidMaps []syscall.SysProcIDMap
	)

	if uidMaps, err = parseIDMapping(uidmap); err != nil {
		return -1, nil, err
	}
	if gidMaps, err = parseIDMapping(gidmap); err != nil {
		return -1, nil, err
	}

	syscall.ForkLock.Lock()
	if err = syscall.Pipe2(pipeMap[:], syscall.O_CLOEXEC); err != nil {
		syscall.ForkLock.Unlock()
		return -1, nil, err
	}

	pid, errno = sys.ForkUserns(pipeMap)
	syscall.ForkLock.Unlock()
	if errno != 0 {
		syscall.Close(pipeMap[0])
		syscall.Close(pipeMap[1])
		return -1, nil, errno
	}

	syscall.Close(pipeMap[0])

	writeMappings := func(fname string, idmap []syscall.SysProcIDMap) error {
		mappings := ""
		for _, m := range idmap {
			mappings = fmt.Sprintf("%d %d %d\n", m.ContainerID, m.HostID, m.Size)
		}
		return os.WriteFile(fmt.Sprintf("/proc/%d/%s", pid, fname), []byte(mappings), 0600)
	}

	cleanUpChild := func() {
		sync := sys.ProcSyncExit
		if _, _, errno := syscall.Syscall6(syscall.SYS_WRITE, uintptr(pipeMap[1]), uintptr(unsafe.Pointer(&sync)), unsafe.Sizeof(sync), 0, 0, 0); errno != 0 {
			logrus.WithError(errno).Warnf("failed to sync with child (ProcSyncExit)")
		}
		syscall.Close(pipeMap[1])

		if _, err := unix.Wait4(int(pid), nil, 0, nil); err != nil {
			logrus.WithError(err).Warnf("failed to wait for child process; the SIGHLD might be received by shim reaper")
		}
	}
	defer cleanUpChild()

	if err := writeMappings("uid_map", uidMaps); err != nil {
		return -1, nil, err
	}
	if err := writeMappings("gid_map", gidMaps); err != nil {
		return -1, nil, err
	}

	if usernsFile, err = os.Open(fmt.Sprintf("/proc/%d/ns/user", pid)); err != nil {
		return -1, nil, fmt.Errorf("failed to get user ns file descriptor for - /proc/%d/user/ns: %w", pid, err)
	}

	return int(usernsFile.Fd()), func() {
		usernsFile.Close()
	}, nil
}
