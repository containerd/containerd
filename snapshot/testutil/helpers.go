package testutil

import (
	"flag"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

var rootEnabled bool

func init() {
	flag.BoolVar(&rootEnabled, "test.root", false, "enable tests that require root")
}

// Unmount unmounts a given mountPoint and sets t.Error if it fails
func Unmount(t *testing.T, mountPoint string) {
	t.Log("unmount", mountPoint)
	if err := syscall.Unmount(mountPoint, 0); err != nil {
		t.Error("Could not umount", mountPoint, err)
	}
}

// RequiresRoot skips tests that require root, unless the test.root flag has
// been set
func RequiresRoot(t *testing.T) {
	if !rootEnabled {
		t.Skip("skipping test that requires root")
		return
	}
	assert.Equal(t, 0, os.Getuid(), "This test must be run as root.")
}
