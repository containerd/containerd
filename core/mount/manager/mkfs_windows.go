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

package manager

import (
	"os"
	"syscall"
)

const fsctlSetSparse = 0x000900c4

// setSparseFile marks a file as sparse on NTFS/ReFS. This must be called
// before Truncate so that extending the file does not zero-fill the
// intervening space on disk, which is extremely slow for large files.
func setSparseFile(f *os.File) error {
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
