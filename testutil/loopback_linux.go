// +build linux

package testutil

import (
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// NewLoopback creates a loopback device, and returns its device name (/dev/loopX), and its clean-up function.
func NewLoopback(t *testing.T, size int64) (string, func()) {
	// create temporary file for the disk image
	file, err := ioutil.TempFile("", "containerd-test-loopback")
	if err != nil {
		t.Fatalf("could not create temporary file for loopback: %v", err)
	}

	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	file.Close()

	// create device
	losetup := exec.Command("losetup", "--find", "--show", file.Name())
	p, err := losetup.Output()
	if err != nil {
		t.Fatal(err)
	}

	deviceName := strings.TrimSpace(string(p))
	t.Logf("Created loop device %s (using %s)", deviceName, file.Name())

	cleanup := func() {
		// detach device
		t.Logf("Removing loop device %s", deviceName)
		losetup := exec.Command("losetup", "--detach", deviceName)
		err := losetup.Run()
		if err != nil {
			t.Error("Could not remove loop device", deviceName, err)
		}

		// remove file
		t.Logf("Removing temporary file %s", file.Name())
		if err = os.Remove(file.Name()); err != nil {
			t.Error(err)
		}
	}

	return deviceName, cleanup
}
