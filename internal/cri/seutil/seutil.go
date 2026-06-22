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

package seutil

import (
	"github.com/opencontainers/selinux/go-selinux"
)

// ChangeToKVM process label
func ChangeToKVM(l string) (string, error) {
	if l == "" || !selinux.GetEnabled() {
		return "", nil
	}
	proc, _ := selinux.KVMContainerLabels()
	selinux.ReleaseLabel(proc)

	current, err := selinux.NewContext(l)
	if err != nil {
		return "", err
	}
	next, err := selinux.NewContext(proc)
	if err != nil {
		return "", err
	}
	current["type"] = next["type"]
	return current.Get(), nil
}
