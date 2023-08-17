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

package opts

import (
	"os"
	"strings"
	"sync"
)

var (
	_cgroupv2HasHugetlbOnce sync.Once
	_cgroupv2HasHugetlb     bool
)

// cgroupv2HasHugetlb returns whether the hugetlb controller is present on
// cgroup v2.
func cgroupv2HasHugetlb() bool {
	_cgroupv2HasHugetlbOnce.Do(func() {
		controllers, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
		if err != nil {
			return
		}
		_cgroupv2HasHugetlb = strings.Contains(string(controllers), "hugetlb")
	})
	return _cgroupv2HasHugetlb
}
