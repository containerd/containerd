// +build windows

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

// Simple wrappers around SetVolumeMountPoint and DeleteVolumeMountPoint

import (
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

// Mount volumePath (in format '\\?\Volume{GUID}' at targetPath.
// https://docs.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-setvolumemountpointw
func setVolumeMountPoint(targetPath string, volumePath string) error {
	if !strings.HasPrefix(volumePath, "\\\\?\\Volume{") {
		return errors.Errorf("unable to mount non-volume path %s", volumePath)
	}

	// Both must end in a backslash
	slashedTarget := filepath.Clean(targetPath) + string(filepath.Separator)
	slashedVolume := volumePath + string(filepath.Separator)

	targetP, err := syscall.UTF16PtrFromString(slashedTarget)
	if err != nil {
		return errors.Wrapf(err, "unable to utf16-ise %s", slashedTarget)
	}

	volumeP, err := syscall.UTF16PtrFromString(slashedVolume)
	if err != nil {
		return errors.Wrapf(err, "unable to utf16-ise %s", slashedVolume)
	}

	if err := windows.SetVolumeMountPoint(targetP, volumeP); err != nil {
		return errors.Wrapf(err, "failed calling SetVolumeMount('%s', '%s')", slashedTarget, slashedVolume)
	}

	return nil
}

// Remove the volume mount at targetPath
// https://docs.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-deletevolumemountpointa
func deleteVolumeMountPoint(targetPath string) error {
	// Must end in a backslash
	slashedTarget := filepath.Clean(targetPath) + string(filepath.Separator)

	targetP, err := syscall.UTF16PtrFromString(slashedTarget)
	if err != nil {
		return errors.Wrapf(err, "unable to utf16-ise %s", slashedTarget)
	}

	if err := windows.DeleteVolumeMountPoint(targetP); err != nil {
		return errors.Wrapf(err, "failed calling DeleteVolumeMountPoint('%s')", slashedTarget)
	}

	return nil
}
