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

package v2

import (
	"os"
	"path/filepath"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// createWorkLink creates a directory junction from bundlePath/work -> work.
// Junctions do not require administrator privileges or Developer Mode,
// unlike symlinks on Windows.
func createWorkLink(work, bundlePath string) error {
	target, err := filepath.Abs(work)
	if err != nil {
		return err
	}

	link := filepath.Join(bundlePath, "work")
	if err := os.Mkdir(link, 0711); err != nil {
		return err
	}

	linkPtr, err := windows.UTF16PtrFromString(link)
	if err != nil {
		os.Remove(link)
		return err
	}

	handle, err := windows.CreateFile(
		linkPtr,
		windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		os.Remove(link)
		return err
	}
	defer windows.CloseHandle(handle)

	rp := winio.ReparsePoint{
		Target:       target,
		IsMountPoint: true,
	}
	data := winio.EncodeReparsePoint(&rp)

	var bytesReturned uint32
	if err := windows.DeviceIoControl(
		handle,
		windows.FSCTL_SET_REPARSE_POINT,
		&data[0],
		uint32(len(data)),
		nil,
		0,
		&bytesReturned,
		nil,
	); err != nil {
		os.Remove(link)
		return err
	}

	return nil
}

// resolveWorkLink reads the junction/symlink at bundlePath/work to recover
// the working directory path. os.Readlink works for both symlinks and
// junctions on Windows.
func resolveWorkLink(bundlePath string) (string, error) {
	return os.Readlink(filepath.Join(bundlePath, "work"))
}
