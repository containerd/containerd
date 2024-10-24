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

package erofs

import (
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/core/mount"
)

func bindLoopDevice(file string) (string, error) {
	loopdev := filepath.Join(filepath.Dir(file), "loop")
	devname, err := os.ReadFile(loopdev)
	if err == nil {
		return string(devname), nil
	}
	loopName, err := mount.AttachLoopDevice(file)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(loopdev, []byte(loopName), 0644); err != nil {
		return "", err
	}
	return loopName, nil
}

func unbindLoopDevice(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "loop")); os.IsNotExist(err) {
		return nil
	}
	devname, err := os.ReadFile(filepath.Join(dir, "loop"))
	if err != nil {
		return err
	}
	return mount.DetachLoopDevice(string(devname))
}
