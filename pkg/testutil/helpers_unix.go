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
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// RequiresRoot skips tests that require root, unless the test.root flag has
// been set
func RequiresRoot(t testing.TB) {
	if !rootEnabled {
		t.Skip("skipping test that requires root")
	}
	assert.Equal(t, 0, os.Getuid(), "This test must be run as root.")
}

// RequiresRootM is similar to RequiresRoot but intended to be called from *testing.M.
func RequiresRootM() {
	if !rootEnabled {
		fmt.Fprintln(os.Stderr, "skipping test that requires root")
		os.Exit(0)
	}
	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "This test must be run as root.")
		os.Exit(1)
	}
}
