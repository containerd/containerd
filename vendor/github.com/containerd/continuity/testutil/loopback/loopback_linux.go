//go:build linux
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
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/containerd/log"
)

// New creates a loopback device
func New(size int64) (*Loopback, error) {
	// create temporary file for the disk image
	file, err := os.CreateTemp("", "containerd-test-loopback")
	if err != nil {
		return nil, fmt.Errorf("could not create temporary file for loopback: %w", err)
	}

	if err := file.Truncate(size); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, fmt.Errorf("failed to resize temp file: %w", err)
	}
	file.Close()

	// create device
	losetup := exec.Command("losetup", "--find", "--show", file.Name())
	var stdout, stderr bytes.Buffer
	losetup.Stdout = &stdout
	losetup.Stderr = &stderr
	if err := losetup.Run(); err != nil {
		os.Remove(file.Name())
		return nil, fmt.Errorf("loopback setup failed (%v): stdout=%q, stderr=%q: %w", losetup.Args, stdout.String(), stderr.String(), err)
	}

	deviceName := strings.TrimSpace(stdout.String())
	log.L.Debugf("Created loop device %s (using %s)", deviceName, file.Name())

	cleanup := func() error {
		// detach device
		log.L.Debugf("Removing loop device %s", deviceName)
		losetup := exec.Command("losetup", "--detach", deviceName)
		if out, err := losetup.CombinedOutput(); err != nil {
			return fmt.Errorf("Could not remove loop device %s (%v): %q: %w", deviceName, losetup.Args, string(out), err)
		}

		// remove file
		log.L.Debugf("Removing temporary file %s", file.Name())
		return os.Remove(file.Name())
	}

	l := Loopback{
		File:   file.Name(),
		Device: deviceName,
		close:  cleanup,
	}
	return &l, nil
}

// Loopback device
type Loopback struct {
	// File is the underlying sparse file
	File string
	// Device is /dev/loopX
	Device string
	close  func() error
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
