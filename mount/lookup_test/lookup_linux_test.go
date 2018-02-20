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

// FIXME: we can't put this test to the mount package:
// import cycle not allowed in test
// package github.com/containerd/containerd/mount (test)
//         imports github.com/containerd/containerd/testutil
//         imports github.com/containerd/containerd/mount
//
// NOTE: we can't have this as lookup_test (compilation fails)
package lookuptest

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/testutil"
	"github.com/gotestyourself/gotestyourself/assert"
)

func checkLookup(t *testing.T, fsType, mntPoint, dir string) {
	t.Helper()
	info, err := mount.Lookup(dir)
	assert.NilError(t, err)
	assert.Equal(t, fsType, info.FSType)
	assert.Equal(t, mntPoint, info.Mountpoint)
}

func testLookup(t *testing.T, fsType string) {
	testutil.RequiresRoot(t)
	mnt, err := ioutil.TempDir("", "containerd-mountinfo-test-lookup")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mnt)

	deviceName, cleanupDevice, err := testutil.NewLoopback(100 << 20) // 100 MB
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mkfs", "-t", fsType, deviceName).CombinedOutput(); err != nil {
		// not fatal
		t.Skipf("could not mkfs (%s) %s: %v (out: %q)", fsType, deviceName, err, string(out))
	}
	if out, err := exec.Command("mount", deviceName, mnt).CombinedOutput(); err != nil {
		// not fatal
		t.Skipf("could not mount %s: %v (out: %q)", deviceName, err, string(out))
	}
	defer func() {
		testutil.Unmount(t, mnt)
		cleanupDevice()
	}()
	assert.Check(t, strings.HasPrefix(deviceName, "/dev/loop"))
	checkLookup(t, fsType, mnt, mnt)

	newMnt, err := ioutil.TempDir("", "containerd-mountinfo-test-newMnt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(newMnt)

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
	lower, err := ioutil.TempDir("", "containerd-mountinfo-test-lower")
	assert.NilError(t, err)
	defer os.RemoveAll(lower)

	upper, err := ioutil.TempDir("", "containerd-mountinfo-test-upper")
	assert.NilError(t, err)
	defer os.RemoveAll(upper)

	work, err := ioutil.TempDir("", "containerd-mountinfo-test-work")
	assert.NilError(t, err)
	defer os.RemoveAll(work)

	overlay, err := ioutil.TempDir("", "containerd-mountinfo-test-overlay")
	assert.NilError(t, err)
	defer os.RemoveAll(overlay)

	if out, err := exec.Command("mount", "-t", "overlay", "overlay", "-o", fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		lower, upper, work), overlay).CombinedOutput(); err != nil {
		// not fatal
		t.Skipf("could not mount overlay: %v (out: %q)", err, string(out))
	}
	defer testutil.Unmount(t, overlay)

	testdir := filepath.Join(overlay, "testdir")
	err = os.Mkdir(testdir, 0777)
	assert.NilError(t, err)

	testfile := filepath.Join(overlay, "testfile")
	_, err = os.Create(testfile)
	assert.NilError(t, err)

	checkLookup(t, "overlay", overlay, testdir)
	checkLookup(t, "overlay", overlay, testfile)
}
