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

package testutil

import (
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// NewLoopback creates a loopback device, and returns its device name (/dev/loopX), and its clean-up function.
func NewLoopback(size int64) (string, func() error, error) {
	// create temporary file for the disk image
	file, err := ioutil.TempFile("", "containerd-test-loopback")
	if err != nil {
		return "", nil, errors.Wrap(err, "could not create temporary file for loopback")
	}

	if err := file.Truncate(size); err != nil {
		file.Close()
		os.Remove(file.Name())
		return "", nil, errors.Wrap(err, "failed to resize temp file")
	}
	file.Close()

	// create device
	losetup := exec.Command("losetup", "--find", "--show", file.Name())
	p, err := losetup.Output()
	if err != nil {
		os.Remove(file.Name())
		return "", nil, errors.Wrap(err, "loopback setup failed")
	}

	deviceName := strings.TrimSpace(string(p))
	logrus.Debugf("Created loop device %s (using %s)", deviceName, file.Name())

	cleanup := func() error {
		// detach device
		logrus.Debugf("Removing loop device %s", deviceName)
		losetup := exec.Command("losetup", "--detach", deviceName)
		err := losetup.Run()
		if err != nil {
			return errors.Wrapf(err, "Could not remove loop device %s", deviceName)
		}

		// remove file
		logrus.Debugf("Removing temporary file %s", file.Name())
		return os.Remove(file.Name())
	}

	return deviceName, cleanup, nil
}
