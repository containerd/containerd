//go:build !linux

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

import specs "github.com/opencontainers/runtime-spec/specs-go"

func defaultProfile(_ *specs.Spec) (*specs.LinuxSeccomp, error) {
	return &specs.LinuxSeccomp{}, nil
}

// DefaultProfile defines the allowed syscalls for the default seccomp profile.
//
// Deprecated: use [WithDefaultProfile] instead, which returns errors from
// generating the profile.
func DefaultProfile(sp *specs.Spec) *specs.LinuxSeccomp {
	return &specs.LinuxSeccomp{}
}
