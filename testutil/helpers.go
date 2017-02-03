package testutil

import (
	"flag"
	"os"
	"path/filepath"
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

// DumpDir will log out all of the contents of the provided directory to
// testing logger.
//
// Use this in a defer statement within tests that may allocate and exercise a
// temporary directory. Immensely useful for sanity checking and debugging
// failing tests.
//
// One should still test that contents are as expected. This is only a visual
// tool to assist when things don't go your way.
func DumpDir(t *testing.T, root string) {
	if err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			t.Log(fi.Mode(), path, "->", target)
		} else {
			t.Log(fi.Mode(), path)
		}

		return nil
	}); err != nil {
		t.Fatalf("error dumping directory: %v", err)
	}
}
