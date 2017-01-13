// Package testutil is a package only used for testing
package testutil

import (
	"os"
	"reflect"
	"runtime"
	"testing"
)

// Requirement is a requirement for executing the test
type Requirement func() bool

// Requires skips the test if requirement is not satisfied
func Requires(t *testing.T, requirements ...Requirement) {
	for _, r := range requirements {
		if !r() {
			s := runtime.FuncForPC(reflect.ValueOf(r).Pointer()).Name()
			t.Skipf("unmatched requirement %s", s)
		}
	}
}

// Privileged is a requirement that requires a privileged user (i.e. root).
func Privileged() bool {
	// NOTE: we could use os/user.Current() for implementing this function.
	// However, we hesitate to call os/user.Current() due to a glibc issue
	// that leads to SEGV when the glibc is statically linked with,
	// although it is unlikely statically linked for test binaries.
	// https://github.com/docker/docker/pull/29478

	// TODO: support Windows
	return os.Getuid() == 0
}
