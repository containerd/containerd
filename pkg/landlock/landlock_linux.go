// +build linux

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

package landlock

import (
	"io/ioutil"
	"os"
	"sync"
	"strings"
)

var (
	landlockSupported bool
	checkLandlock     sync.Once
)

// hostSupports returns true if a is enabled for on the host
func hostSupports() bool {
	checkLandlock.Do(func() {
		landlockSupported = false
		if _, err := os.Stat("/sys/kernel/security/lsm"); err == nil {
			buf, err := ioutil.ReadFile("/sys/kernel/security/lsm");
			if err == nil {
				lsm_str := string(buf)
				lsm_slice := strings.Split(lsm_str, ",")
				for _ , lsm := range lsm_slice {
					if lsm == "landlock" {
						landlockSupported = true
					}  
				}
			}
		}	
	})
	return landlockSupported
}
