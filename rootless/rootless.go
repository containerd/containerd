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

package rootless

import (
	"os"
)

var (
	// RunningWithNonRootUsername is set to true if we $USER is set to a non-root value.
	// Note that this variable is set to true even when EUID is 0, typically when
	// running in a user namespace.
	//
	// The value of this variable is mostly used for configuring default paths.
	// If the value is true, $HOME and $XDG_RUNTIME_DIR should be honored for setting up the default paths.
	// If false (not only EUID==0 but also $USER==root), $HOME and $XDG_RUNTIME_DIR should be ignored
	// even if we are in a user namespace.
	RunningWithNonRootUsername bool
)

func init() {
	u := os.Getenv("USER")
	RunningWithNonRootUsername = u != "" && u != "root"
}
