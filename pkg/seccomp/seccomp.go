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

package seccomp

// IsEnabled returns whether seccomp support is enabled
// On Linux returns if the kernel has been configured to support seccomp.
//  From https://github.com/opencontainers/runc/blob/v1.0.0-rc91/libcontainer/seccomp/seccomp_linux.go#L86-L102
// On non-Linux returns false
func IsEnabled() bool {
	return isEnabled()
}
