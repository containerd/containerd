//go:build linux

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

package blkdiscard

import "os/exec"

// Version returns the output of "blkdiscard --version"
func Version() (string, error) {
	return blkdiscard("--version")
}

// BlkDiscard discards all blocks of a device.
// devicePath is expected to be a fully qualified path.
// BlkDiscard expects the caller to verify that the device is not in use.
func BlkDiscard(devicePath string) (string, error) {
	return blkdiscard(devicePath)
}

func blkdiscard(args ...string) (string, error) {
	output, err := exec.Command("blkdiscard", args...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
