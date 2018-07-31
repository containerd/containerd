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

package encryption

import (
	"fmt"
)

// Uint64ToStringArray converts an array of uint64's to an array of strings
// by applying a format string to each uint64
func Uint64ToStringArray(format string, in []uint64) []string {
	var ret []string

	for _, v := range in {
		ret = append(ret, fmt.Sprintf(format, v))
	}
	return ret
}
