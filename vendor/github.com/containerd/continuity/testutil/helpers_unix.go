//go:build !windows

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
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// Unmount unmounts a given mountPoint and sets t.Error if it fails
func Unmount(t *testing.T, mountPoint string) {
	t.Log("unmount", mountPoint)
	if err := unmountAll(mountPoint); err != nil {
		t.Error("Could not umount", mountPoint, err)
	}
}

// RequiresRoot skips tests that require root, unless the test.root flag has
// been set
func RequiresRoot(t testing.TB) {
	if !rootEnabled {
		t.Skip("skipping test that requires root")
		return
	}
	if os.Getuid() != 0 {
		t.Error("This test must be run as root.")
	}
}

func unmountAll(mountpoint string) error {
	for {
		if err := unix.Unmount(mountpoint, unmountFlags); err != nil {
			if err == unix.EINVAL {
				return nil
			}
			return err
		}
	}
}
