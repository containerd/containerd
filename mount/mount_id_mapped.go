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
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// TODO: Support multiple mappings in future
func parseIdMapping(mapping string) ([]syscall.SysProcIDMap, error) {
	parts := strings.Split(mapping, ":")
	if len(parts) != 3 {
		return []syscall.SysProcIDMap{}, errors.New("user namespace mappings require the format `container-id:host-id:size`")
	}
	cID, err := strconv.ParseUint(parts[0], 0, 32)
	if err != nil {
		return []syscall.SysProcIDMap{}, errors.Wrapf(err, "invalid container id for user namespace remapping")
	}
	hID, err := strconv.ParseUint(parts[1], 0, 32)
	if err != nil {
		return []syscall.SysProcIDMap{}, errors.Wrapf(err, "invalid host id for user namespace remapping")
	}
	size, err := strconv.ParseUint(parts[2], 0, 32)
	if err != nil {
		return []syscall.SysProcIDMap{}, errors.Wrapf(err, "invalid size for user namespace remapping")
	}

	return []syscall.SysProcIDMap{
		{
			ContainerID: int(cID),
			HostID:      int(hID),
			Size:        int(size),
		},
	}, nil
}

func mountIdMapped(target string, pid int) (err error) {
	var (
		path       string
		attr       MountAttr
		userNsFile *os.File
		targetDir  *os.File
	)

	path = fmt.Sprintf("/proc/%d/ns/user", pid)
	if userNsFile, err = os.OpenFile(path, os.O_RDONLY, 0); err != nil {
		return errors.Wrapf(err, "Unable to get user ns file descriptor for - %s\n", path)
	}

	attr.attr_set = MOUNT_ATTR_IDMAP
	attr.userns = uint64(userNsFile.Fd())

	defer userNsFile.Close()
	if targetDir, err = os.OpenFile(target, os.O_RDONLY, 0); err != nil {
		return errors.Wrapf(err, "Unable to get mount point target file descriptor - %s\n", target)
	}

	defer targetDir.Close()
	return MountSetAttr(int(targetDir.Fd()), "", unix.AT_EMPTY_PATH|AT_RECURSIVE,
		&attr, uint(unsafe.Sizeof(attr)))
}

func MapMount(uidmap string, gidmap string, target string) (err error) {
	const (
		userNsHelperBinary = "/bin/sh"
	)
	// TODO: Avoid dependency on /bin/sh or do in a complete different way
	// Currently there is no way to pass idmapping directly to mount_setattr,
	// this is not very convinient from the container runtime point of view.
	// The id remapping procedure should be done in containerd, due to we have
	// old approach the use recurcive chown.
	// Maybe it is necessary to think about moving of container rootfs ownership
	// adjustment to runc due to runc have a knowledge about container user namespace.
	// But personally I think that it would be better to add possibility to call
	// mount_setattr with explicit id mappings and leave container runtime components
	// responcibilities unchanged.
	cmd := exec.Command(userNsHelperBinary)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER,
	}

	if cmd.SysProcAttr.UidMappings, err = parseIdMapping(uidmap); err != nil {
		return err
	}
	if cmd.SysProcAttr.GidMappings, err = parseIdMapping(gidmap); err != nil {
		return err
	}

	if err = cmd.Start(); err != nil {
		return errors.Wrapf(err, "Failed to run the %s helper binary\n", userNsHelperBinary)
	}

	if err = mountIdMapped(target, cmd.Process.Pid); err != nil {
		return errors.Wrapf(err, "Failed to create idmapped mount for target - %s\n", target)
	}

	if err = cmd.Wait(); err != nil {
		return errors.Wrapf(err, "Failed to run the %s helper binary\n", userNsHelperBinary)
	}

	return nil
}
