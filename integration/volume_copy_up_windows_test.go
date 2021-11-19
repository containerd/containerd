//go:build windows
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

package integration

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func getVolumeHostPathOwnership(criRoot, containerID string) (string, error) {
	hostPath := fmt.Sprintf("%s/containers/%s/volumes/", criRoot, containerID)
	if _, err := os.Stat(hostPath); err != nil {
		return "", err
	}

	volumes, err := ioutil.ReadDir(hostPath)
	if err != nil {
		return "", err
	}

	if len(volumes) != 1 {
		return "", fmt.Errorf("expected to find exactly 1 volume (got %d)", len(volumes))
	}

	secInfo, err := windows.GetNamedSecurityInfo(
		filepath.Join(hostPath, volumes[0].Name()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)

	if err != nil {
		return "", err
	}

	sid, _, err := secInfo.Owner()
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}
