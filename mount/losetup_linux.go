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
	"math/rand"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/pkg/errors"
)

const (
	loopControlPath = "/dev/loop-control"
	loopDevFormat   = "/dev/loop%d"

	// According to util-linux/include/loopdev.h
	ioctlSetFd       = 0x4C00
	ioctlClrFd       = 0x4C01
	ioctlSetStatus64 = 0x4C04
	ioctlGetFree     = 0x4C82

	loFlagsReadonly = 1
	//loFlagsUseAops   = 2
	loFlagsAutoclear = 4
	//loFlagsPartScan  = 8
	loFlagsDirectIO = 16

	ebusyString = "device or resource busy"
)

// parameters to control loop device setup
type LoopParams struct {
	// Loop device should forbid write
	Readonly bool
	// Loop device is automatically cleared by kernel when the
	// last opener closes it
	Autoclear bool
	// Use direct IO to access the loop backing file
	Direct bool
}

// struct loop_info64 in util-linux/include/loopdev.h
type loopInfo struct {
	/*
		device         uint64
		inode          uint64
		rdevice        uint64
		offset         uint64
		sizelimit      uint64
		number         uint32
		encryptType    uint32
		encryptKeySize uint32
	*/
	_        [13]uint32
	flags    uint32
	fileName [64]byte
	/*
		cryptName  [64]byte
		encryptKey [32]byte
		init       [2]uint64
	*/
	_ [112]byte
}

func ioctl(fd, req, args uintptr) (uintptr, uintptr, error) {
	r1, r2, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, args)
	if errno != 0 {
		return 0, 0, errno
	}

	return r1, r2, nil
}

func getFreeLoopDev() (uint32, error) {
	ctrl, err := os.OpenFile(loopControlPath, os.O_RDWR, 0)
	if err != nil {
		return 0, errors.Errorf("could not open %v: %v", loopControlPath, err)
	}
	defer ctrl.Close()
	num, _, err := ioctl(ctrl.Fd(), ioctlGetFree, 0)
	if err != nil {
		return 0, errors.Wrap(err, "could not get free loop device")
	}
	return uint32(num), nil
}

func setupLoopDev(backingFile, loopDev string, param LoopParams) (devFile *os.File, err error) {
	// 1. Open backing file and loop device
	oflags := os.O_RDWR
	if param.Readonly {
		oflags = os.O_RDONLY
	}
	back, err := os.OpenFile(backingFile, oflags, 0)
	if err != nil {
		return nil, errors.Errorf("could not open backing file: %v", err)
	}
	defer back.Close()

	loopFile, err := os.OpenFile(loopDev, oflags, 0)
	if err != nil {
		return nil, errors.Errorf("could not open loop device: %v", err)
	}
	defer func() {
		if err != nil {
			loopFile.Close()
		}
	}()

	// 2. Set FD
	if _, _, err = ioctl(loopFile.Fd(), ioctlSetFd, back.Fd()); err != nil {
		return nil, errors.Errorf("could not set loop fd: %v", err)
	}

	// 3. Set Info
	info := loopInfo{}
	copy(info.fileName[:], []byte(backingFile))
	if param.Readonly {
		info.flags |= loFlagsReadonly
	}
	if param.Autoclear {
		info.flags |= loFlagsAutoclear
	}
	if param.Direct {
		info.flags |= loFlagsAutoclear
	}
	if _, _, err := ioctl(loopFile.Fd(), ioctlSetStatus64, uintptr(unsafe.Pointer(&info))); err != nil {
		// Retry w/o direct IO flag in case kernel does not support it. The downside is that
		// it will suffer from double cache problem.
		info.flags &= ^(uint32(loFlagsDirectIO))
		if _, _, err := ioctl(loopFile.Fd(), ioctlSetStatus64, uintptr(unsafe.Pointer(&info))); err != nil {
			ioctl(loopFile.Fd(), ioctlClrFd, 0)
			return nil, errors.Errorf("cannot set loop info:%v", err)
		}
	}

	return loopFile, nil
}

// setupLoop looks for (and possibly creates) a free loop device, and
// then attaches backingFile to it.
//
// When autoclear is true, caller should take care to close it when
// done with the loop device. The loop device file handle keeps
// loFlagsAutoclear in effect and we rely on it to clean up the loop
// device. If caller closes the file handle after mounting the device,
// kernel will clear the loop device after it is umounted. Otherwise
// the loop device is cleared when the file handle is closed.
//
// When autoclear is false, caller should be responsible to remove
// the loop device when done with it.
//
// Upon success, the file handle to the loop device is returned.
func setupLoop(backingFile string, param LoopParams) (*os.File, error) {
	var loopDev string

	for retry := 1; retry < 200; retry++ {
		num, err := getFreeLoopDev()
		if err != nil {
			return nil, err
		}

		loopDev = fmt.Sprintf(loopDevFormat, num)
		loopFile, err := setupLoopDev(backingFile, loopDev, param)
		if err != nil {
			// Per util-linux/sys-utils/losetup.c:create_loop(),
			// free loop device can race and we end up failing
			// with EBUSY when trying to set it up.
			if strings.Contains(err.Error(), ebusyString) {
				// Fallback a bit to avoid live lock
				time.Sleep(time.Millisecond * time.Duration(rand.Intn(retry*10)))
				continue
			}
			return nil, err
		}
		return loopFile, nil
	}

	return nil, errors.New("Timeout creating new loopback device")
}

func removeLoop(loopdev string) error {
	dev, err := os.Open(loopdev)
	if err != nil {
		return err
	}
	_, _, err = ioctl(dev.Fd(), ioctlClrFd, 0)
	dev.Close()
	return err
}

// Attach a specified backing file to a loop device
func AttachLoopDevice(backingFile string) (string, error) {
	dev, err := setupLoop(backingFile, LoopParams{})
	if err != nil {
		return "", err
	}
	dev.Close()

	return dev.Name(), nil
}

// Detach a loop device
func DetachLoopDevice(devices ...string) error {
	for _, dev := range devices {
		if err := removeLoop(dev); err != nil {
			return err
		}
	}

	return nil
}
