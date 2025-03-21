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

package quota

import (
	"fmt"
	"os"
	"reflect"
	"syscall"
	"unsafe"
)

// xfs ioctl command
const (
	flagProjInhert = 0x200
)

// reference: ioctl.h
// ioctl dir
type dir uint32

const (
	readDir  dir = 0x2
	writeDir dir = 0x1
)

// ioctl magic number
type magciNumber uint32

const (
	xNumber magciNumber = 0x58
)

// ioctl command number
type commandNumber uint32

const (
	readCmd  commandNumber = 0x1f
	writeCmd commandNumber = 0x20
)

// ioctl offset
const (
	dirShift         = 30
	sizeShift        = 16
	magicNumberShift = 8
	cmdShift         = 0
)

var (
	fsGetXattr = io(readDir, xNumber, readCmd, fsxattr{})
	fsSetXattr = io(writeDir, xNumber, writeCmd, fsxattr{})
)

//nolint:structcheck
type fsxattr struct {
	flags     uint32
	extsize   uint32
	nextents  uint32
	projectid uint32
	padder    [12]uint8
}

func io(d dir, m magciNumber, nr commandNumber, v interface{}) uint32 {
	typev := reflect.TypeOf(v)
	if typev == nil {
		panic("should not be null")
	}
	size := uint32(typev.Size())

	return uint32(d)<<dirShift |
		(size)<<sizeShift |
		uint32(m)<<magicNumberShift |
		uint32(nr)<<cmdShift

}

// getProjectID - get the project id of path on xfs
func getProjectID(targetPath string) (uint32, error) {
	dir, err := os.Open(targetPath)
	if err != nil {
		return 0, err
	}
	defer dir.Close()

	var fsx fsxattr

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dir.Fd(), uintptr(fsGetXattr),
		uintptr(unsafe.Pointer(&fsx)))
	if errno != 0 {
		return 0, fmt.Errorf("Failed to get projid for %s: %v", targetPath, errno.Error())
	}

	return fsx.projectid, nil
}

// setProjectID - set the project id of path on xfs
func setProjectID(targetPath string, projectID uint32) error {
	dir, err := os.Open(targetPath)
	if err != nil {
		return err
	}
	defer dir.Close()

	var fsx fsxattr
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dir.Fd(), uintptr(fsGetXattr),
		uintptr(unsafe.Pointer(&fsx)))
	if errno != 0 {
		return fmt.Errorf("Failed to get projid for %s: %v", targetPath, errno.Error())
	}
	fsx.projectid = projectID
	fsx.flags |= flagProjInhert
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, dir.Fd(), uintptr(fsSetXattr),
		uintptr(unsafe.Pointer(&fsx)))
	if errno != 0 {
		return fmt.Errorf("Failed to set projid for %s: %v", targetPath, errno.Error())
	}

	return nil
}
