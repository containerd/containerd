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

package loopback

import (
	"io/ioutil"
	"os"
	"os/exec"
	"syscall"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// New creates a loopback device
func New(size int64) (*Loopback, error) {
	// create temporary file for the disk image
	file, err := ioutil.TempFile("", "containerd-test-loopback")
	if err != nil {
		return nil, errors.Wrap(err, "could not create temporary file for loopback")
	}

	if err := file.Truncate(size); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, errors.Wrap(err, "failed to resize temp file")
	}
	file.Close()

	// create device
	losetup := exec.Command("losetup", "--find", "--show", file.Name())
	p, err := losetup.Output()
	if err != nil {
		os.Remove(file.Name())
		return nil, errors.Wrap(err, "loopback setup failed")
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

	l := Loopback{
		File: file.Name(),
		Device: deviceName,
		close: cleanup,
	}
	return &l, nil
}

// Loopback device
type Loopback struct {
	// File is the underlying sparse file
	File string
	// Device is /dev/loopX
	Device string
	close func() error
}

// SoftSize returns st_size
func (l *Loopback) SoftSize() (int64, error) {
	st, err := os.Stat(l.File)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// HardSize returns st_blocks * 512; see stat(2)
func (l *Loopback) HardSize() (int64, error) {
	st, err := os.Stat(l.File)
	if err != nil {
		return 0, err
	}
	st2, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("st.Sys() is not a *syscall.Stat_t")
	}
	// NOTE: st_blocks has nothing to do with st_blksize; see stat(2)
	return st2.Blocks * 512, nil
}

// Close detaches the device and removes the underlying file
func (l *Loopback) Close() error {
	return l.close()
}
