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

package mount

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	// containerd/pkg/testutil has circular dependency on this mount pkg.
	// so we use continuity/testutil instead.
	"github.com/containerd/continuity/testutil"
	"github.com/containerd/continuity/testutil/loopback"
	"github.com/stretchr/testify/assert"
)

func checkLookup(t *testing.T, fsType, mntPoint, dir string) {
	t.Helper()
	info, err := Lookup(dir)
	assert.NoError(t, err)
	assert.Equal(t, fsType, info.FSType)
	assert.Equal(t, mntPoint, info.Mountpoint)
}

func testLookup(t *testing.T, fsType string) {
	testutil.RequiresRoot(t)
	mnt := t.TempDir()

	loop, err := loopback.New(100 << 20) // 100 MB
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mkfs", "-t", fsType, loop.Device).CombinedOutput(); err != nil {
		// not fatal
		loop.Close()
		t.Skipf("could not mkfs (%s) %s: %v (out: %q)", fsType, loop.Device, err, string(out))
	}
	if out, err := exec.Command("mount", loop.Device, mnt).CombinedOutput(); err != nil {
		// not fatal
		loop.Close()
		t.Skipf("could not mount %s: %v (out: %q)", loop.Device, err, string(out))
	}
	defer func() {
		testutil.Unmount(t, mnt)
		loop.Close()
	}()
	assert.True(t, strings.HasPrefix(loop.Device, "/dev/loop"))
	checkLookup(t, fsType, mnt, mnt)

	newMnt := t.TempDir()

	if out, err := exec.Command("mount", "--bind", mnt, newMnt).CombinedOutput(); err != nil {
		t.Fatalf("could not mount %s to %s: %v (out: %q)", mnt, newMnt, err, string(out))
	}
	defer func() {
		testutil.Unmount(t, newMnt)
	}()
	checkLookup(t, fsType, newMnt, newMnt)

	subDir := filepath.Join(newMnt, "subDir")
	err = os.MkdirAll(subDir, 0700)
	if err != nil {
		t.Fatal(err)
	}
	checkLookup(t, fsType, newMnt, subDir)
}

func TestLookupWithExt4(t *testing.T) {
	testLookup(t, "ext4")
}

func TestLookupWithXFS(t *testing.T) {
	testLookup(t, "xfs")
}

func TestLookupWithOverlay(t *testing.T) {
	lower := t.TempDir()
	upper := t.TempDir()
	work := t.TempDir()
	overlay := t.TempDir()

	if out, err := exec.Command("mount", "-t", "overlay", "overlay", "-o", fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		lower, upper, work), overlay).CombinedOutput(); err != nil {
		// not fatal
		t.Skipf("could not mount overlay: %v (out: %q)", err, string(out))
	}
	defer testutil.Unmount(t, overlay)

	testdir := filepath.Join(overlay, "testdir")
	err := os.Mkdir(testdir, 0777)
	assert.NoError(t, err)

	testfile := filepath.Join(overlay, "testfile")
	_, err = os.Create(testfile)
	assert.NoError(t, err)

	checkLookup(t, "overlay", overlay, testdir)
	checkLookup(t, "overlay", overlay, testfile)
}
