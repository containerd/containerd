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

package erofsutils

import (
	"os"
	"syscall"
)

const fsctlSetSparse = 0x000900c4 // FSCTL_SET_SPARSE

// ensureSparseLayerFile creates path if missing and marks it sparse so
// mkfs.erofs's 2 TiB ftruncate of the output file does not zero-fill on
// NTFS/ReFS. See erofs-utils lib/diskbuf.c::erofs_diskbuf_init.
func ensureSparseLayerFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	var bytesReturned uint32
	return syscall.DeviceIoControl(
		syscall.Handle(f.Fd()),
		fsctlSetSparse,
		nil, 0,
		nil, 0,
		&bytesReturned,
		nil,
	)
}
