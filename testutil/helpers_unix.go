// +build !windows

package testutil

import (
	"fmt"
	"os"
	"testing"

	"github.com/containerd/containerd/mount"
	"github.com/stretchr/testify/assert"
)

// Unmount unmounts a given mountPoint and sets t.Error if it fails
func Unmount(t *testing.T, mountPoint string) {
	t.Log("unmount", mountPoint)
	if err := mount.UnmountAll(mountPoint, umountflags); err != nil {
		t.Error("Could not umount", mountPoint, err)
	}
}

const enableRootTestsEnvVar = "ENABLE_ROOT_TESTS"

// RequiresRoot skips tests that require root, unless the test.root flag has
// been set
func RequiresRoot(t testing.TB) {
	if os.Getenv(enableRootTestsEnvVar) == "" {
		t.Skipf("test requires root, set %s=1 to run this test", enableRootTestsEnvVar)
		return
	}
	assert.Equal(t, 0, os.Getuid(), "This test must be run as root.")
}

// RequiresRootM is similar to RequiresRoot but intended to be called from *testing.M.
func RequiresRootM() {
	if os.Getenv(enableRootTestsEnvVar) == "" {
		fmt.Fprintf(os.Stderr, "test requires root, set %s=1 to run this test\n", enableRootTestsEnvVar)
		os.Exit(0)
	}
	if 0 != os.Getuid() {
		fmt.Fprintln(os.Stderr, "This test must be run as root.")
		os.Exit(1)
	}
}
