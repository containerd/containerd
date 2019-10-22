// +build linux

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

package genetlink

import (
	"encoding/binary"
	"os"
	"sync"
	"unsafe"
)

var (
	sysEndian binary.ByteOrder
	sysOnce   sync.Once

	sysPageSize = os.Getpagesize()
)

// getSysEndian returns byte order in current host.
func getSysEndian() binary.ByteOrder {
	sysOnce.Do(func() {
		buf := [2]byte{}
		*(*uint16)(unsafe.Pointer(&buf[0])) = uint16(0x1234)

		switch buf[0] {
		case 0x34:
			sysEndian = binary.LittleEndian
		default:
			sysEndian = binary.BigEndian
		}
	})
	return sysEndian
}
