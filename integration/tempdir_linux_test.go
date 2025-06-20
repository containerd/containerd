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
	"os"
	"os/exec"
	"testing"

	"github.com/containerd/continuity/testutil/loopback"
)

type workDir struct {
	loopFile *loopback.Loopback
	mntDir   string
}

// prepareWorkDir creates a loop device, formats it, and mounts it to a temporary directory.
// sizeMB specifies the size of the loop device in megabytes.
func prepareWorkDir(t *testing.T, bytezise int64) (*workDir, error) {
	loop, err := loopback.New(bytezise)
	if err != nil {
		return nil, fmt.Errorf("could not create loop device: %v", err)
	}

	if out, err := exec.Command("mkfs.xfs", "-m", "crc=0", "-n", "ftype=1", loop.Device).CombinedOutput(); err != nil {
		// not fatal
		loop.Close()
		return nil, fmt.Errorf("could not mkfs %s: %v (out: %q)", loop.Device, err, string(out))
	}
	mntDir := t.TempDir()

	if out, err := exec.Command("mount", loop.Device, mntDir).CombinedOutput(); err != nil {
		// not fatal
		loop.Close()
		os.RemoveAll(mntDir)
		return nil, fmt.Errorf("could not mount %s: %v (out: %q)", loop.Device, err, string(out))
	}

	return &workDir{
		loopFile: loop,
		mntDir:   mntDir,
	}, nil
}

// Dir returns the path to the mounted temporary directory.
func (wd *workDir) Dir() string {
	return wd.mntDir
}

// Close unmounts the loop device and deletes the loop file and mount directory.
func (wd *workDir) Close() error {
	// Unmount the mount point.
	umountCmd := exec.Command("umount", wd.mntDir)
	if err := umountCmd.Run(); err != nil {
		return fmt.Errorf("umount command failed: %w", err)
	}
	return wd.loopFile.Close()
}
