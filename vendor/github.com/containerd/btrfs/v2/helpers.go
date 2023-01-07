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

package btrfs

/*
#include "btrfs.h"
*/
import "C"

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/cpu"
)

func subvolID(fd uintptr) (uint64, error) {
	var args C.struct_btrfs_ioctl_ino_lookup_args
	args.objectid = C.BTRFS_FIRST_FREE_OBJECTID

	if err := ioctl(fd, C.BTRFS_IOC_INO_LOOKUP, uintptr(unsafe.Pointer(&args))); err != nil {
		return 0, err
	}

	return uint64(args.treeid), nil
}

var (
	zeroArray = [16]byte{}
	zeros     = zeroArray[:]
)

func uuidString(uuid *[C.BTRFS_UUID_SIZE]C.__u8) string {
	b := (*[maxByteSliceSize]byte)(unsafe.Pointer(uuid))[:C.BTRFS_UUID_SIZE]

	if bytes.Equal(b, zeros) {
		return ""
	}

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:4+2], b[6:6+2], b[8:8+2], b[10:16])
}

func le16ToNative(le16 C.__le16) uint16 {
	if cpu.IsBigEndian {
		b := make([]byte, 2)
		binary.LittleEndian.PutUint16(b, uint16(le16))
		return binary.BigEndian.Uint16(b)
	}
	return uint16(le16)
}

func le64ToNative(le64 C.__le64) uint64 {
	if cpu.IsBigEndian {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, uint64(le64))
		return binary.BigEndian.Uint64(b)
	}
	return uint64(le64)
}

func findMountPoint(path string) (string, error) {
	fp, err := os.Open("/proc/self/mounts")
	if err != nil {
		return "", err
	}
	defer fp.Close()

	const (
		deviceIdx = 0
		pathIdx   = 1
		typeIdx   = 2
		options   = 3
	)

	var (
		mount   string
		scanner = bufio.NewScanner(fp)
	)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if fields[typeIdx] != "btrfs" {
			continue // skip non-btrfs
		}

		if strings.HasPrefix(path, fields[pathIdx]) {
			mount = fields[pathIdx]
		}
	}

	if scanner.Err() != nil {
		return "", scanner.Err()
	}

	if mount == "" {
		return "", fmt.Errorf("mount point of %v not found", path)
	}

	return mount, nil
}
