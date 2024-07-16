//go:build !windows && !darwin

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

package oci

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/moby/sys/user/userns"
	"github.com/stretchr/testify/assert"
)

func cleanupTest() {
	overrideDeviceFromPath = nil
	osReadDir = os.ReadDir
	usernsRunningInUserNS = userns.RunningInUserNS
}

// Based on test from runc:
// https://github.com/opencontainers/runc/blob/v1.0.0/libcontainer/devices/device_unix_test.go#L34-L47
func TestHostDevicesOSReadDirFailure(t *testing.T) {
	testError := fmt.Errorf("test error: %w", os.ErrPermission)

	// Override os.ReadDir to inject error.
	osReadDir = func(dirname string) ([]os.DirEntry, error) {
		return nil, testError
	}

	// Override userns.RunningInUserNS to ensure not running in user namespace.
	usernsRunningInUserNS = func() bool {
		return false
	}
	defer cleanupTest()

	_, err := HostDevices()
	if !errors.Is(err, testError) {
		t.Fatalf("Unexpected error %v, expected %v", err, testError)
	}
}

// Based on test from runc:
// https://github.com/opencontainers/runc/blob/v1.0.0/libcontainer/devices/device_unix_test.go#L34-L47
func TestHostDevicesOSReadDirFailureInUserNS(t *testing.T) {
	testError := fmt.Errorf("test error: %w", os.ErrPermission)

	// Override os.ReadDir to inject error.
	osReadDir = func(dirname string) ([]os.DirEntry, error) {
		if dirname == "/dev" {
			fi, err := os.Lstat("/dev/null")
			if err != nil {
				t.Fatalf("Unexpected error %v", err)
			}

			return []os.DirEntry{fileInfoToDirEntry(fi)}, nil
		}
		return nil, testError
	}
	// Override userns.RunningInUserNS to ensure running in user namespace.
	usernsRunningInUserNS = func() bool {
		return true
	}
	defer cleanupTest()

	_, err := HostDevices()
	if !errors.Is(err, nil) {
		t.Fatalf("Unexpected error %v, expected %v", err, nil)
	}
}

// Based on test from runc:
// https://github.com/opencontainers/runc/blob/v1.0.0/libcontainer/devices/device_unix_test.go#L49-L74
func TestHostDevicesDeviceFromPathFailure(t *testing.T) {
	testError := fmt.Errorf("test error: %w", os.ErrPermission)

	// Override DeviceFromPath to produce an os.ErrPermission on /dev/null.
	overrideDeviceFromPath = func(path string) error {
		if path == "/dev/null" {
			return testError
		}
		return nil
	}

	// Override userns.RunningInUserNS to ensure not running in user namespace.
	usernsRunningInUserNS = func() bool {
		return false
	}
	defer cleanupTest()

	d, err := HostDevices()
	if !errors.Is(err, testError) {
		t.Fatalf("Unexpected error %v, expected %v", err, testError)
	}

	assert.Equal(t, 0, len(d))
}

// Based on test from runc:
// https://github.com/opencontainers/runc/blob/v1.0.0/libcontainer/devices/device_unix_test.go#L49-L74
func TestHostDevicesDeviceFromPathFailureInUserNS(t *testing.T) {
	testError := fmt.Errorf("test error: %w", os.ErrPermission)

	// Override DeviceFromPath to produce an os.ErrPermission on all devices,
	// except for /dev/null.
	overrideDeviceFromPath = func(path string) error {
		if path == "/dev/null" {
			return nil
		}
		return testError
	}

	// Override userns.RunningInUserNS to ensure running in user namespace.
	usernsRunningInUserNS = func() bool {
		return true
	}
	defer cleanupTest()

	d, err := HostDevices()
	if !errors.Is(err, nil) {
		t.Fatalf("Unexpected error %v, expected %v", err, nil)
	}
	assert.Equal(t, 1, len(d))
	assert.Equal(t, d[0].Path, "/dev/null")
}

func TestHostDevicesAllValid(t *testing.T) {
	devices, err := HostDevices()
	if err != nil {
		t.Fatalf("failed to get host devices: %v", err)
	}

	for _, device := range devices {
		if runtime.GOOS != "freebsd" {
			// On Linux, devices can't have major number 0.
			if device.Major == 0 {
				t.Errorf("device entry %+v has zero major number", device)
			}
		}
		switch device.Type {
		case blockDevice, charDevice:
		case fifoDevice:
			t.Logf("fifo devices shouldn't show up from HostDevices")
			fallthrough
		default:
			t.Errorf("device entry %+v has unexpected type %v", device, device.Type)
		}
	}
}
