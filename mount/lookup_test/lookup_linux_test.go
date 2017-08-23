// +build linux

// FIXME: we can't put this test to the mount package:
// import cycle not allowed in test
// package github.com/containerd/containerd/mount (test)
//         imports github.com/containerd/containerd/testutil
//         imports github.com/containerd/containerd/mount
//
// NOTE: we can't have this as lookup_test (compilation fails)
package lookuptest

import (
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/testutil"
	"github.com/stretchr/testify/assert"
)

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
	assert.True(t, strings.HasPrefix(deviceName, "/dev/loop"))
	info, err := mount.Lookup(mnt)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, fsType, info.FSType)
}

func TestLookupWithExt4(t *testing.T) {
	testLookup(t, "ext4")
}

func TestLookupWithXFS(t *testing.T) {
	testLookup(t, "xfs")
}
