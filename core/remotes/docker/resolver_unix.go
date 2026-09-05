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

package docker

import (
	"errors"
	"syscall"
)

func isConnError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

// isConnResetError reports whether err represents a connection reset by
// peer. The errno typically arrives wrapped in *net.OpError and
// *os.SyscallError; errors.Is unwraps it.
func isConnResetError(err error) bool {
	return errors.Is(err, syscall.ECONNRESET)
}
