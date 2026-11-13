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

package proxy

import (
	"os"

	"golang.org/x/sys/windows"
)

// openRegular opens p for reading, sharing it for deletion. os.Open does not
// share a file for deletion, so the remote store, a separate process, could
// not delete or rename a blob while containerd reads it.
//
// Named pipes are outside the path namespace a blobs directory addresses, so
// the open cannot block on one.
func openRegular(p string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	return os.NewFile(uintptr(h), p), nil
}
