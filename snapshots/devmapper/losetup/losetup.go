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

package losetup

import (
	"os/exec"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// FindAssociatedLoopDevices returns a list of loop devices attached to a given image
func FindAssociatedLoopDevices(imagePath string) ([]string, error) {
	output, err := losetup("--list", "--output", "NAME", "--associated", imagePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get loop devices: '%s'", output)
	}

	if output == "" {
		return []string{}, nil
	}

	items := strings.Split(output, "\n")
	if len(items) <= 1 {
		return []string{}, nil
	}

	// Skip header with column names
	return items[1:], nil
}

// AttachLoopDevice finds first available loop device and associates it with an image.
func AttachLoopDevice(imagePath string) (string, error) {
	return losetup("--find", "--show", imagePath)
}

// DetachLoopDevice detaches loop devices
func DetachLoopDevice(loopDevice ...string) error {
	args := append([]string{"--detach"}, loopDevice...)
	_, err := losetup(args...)
	return err
}

// RemoveLoopDevicesAssociatedWithImage detaches all loop devices attached to a given sparse image
func RemoveLoopDevicesAssociatedWithImage(imagePath string) error {
	loopDevices, err := FindAssociatedLoopDevices(imagePath)
	if err != nil {
		return err
	}

	for _, loopDevice := range loopDevices {
		if err = DetachLoopDevice(loopDevice); err != nil && err != unix.ENOENT {
			return err
		}
	}

	return nil
}

// losetup is a wrapper around losetup command line tool
func losetup(args ...string) (string, error) {
	cmd := exec.Command("losetup", args...)
	cmd.Env = append(cmd.Env, "LANG=C")
	data, err := cmd.CombinedOutput()
	output := string(data)
	if err != nil {
		if strings.Contains(output, "No such file or directory") || strings.Contains(output, "No such device") {
			return "", unix.ENOENT
		}

		return "", errors.Wrapf(err, "losetup %s\nerror: %s\n", strings.Join(args, " "), output)
	}

	return strings.TrimSuffix(output, "\n"), err
}
