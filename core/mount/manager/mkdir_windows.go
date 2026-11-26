//go:build windows

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

package manager

import (
	"path/filepath"
	"strings"
)

// splitMkdirPathValue splits value, an X-containerd.mkdir.path
// option's value, into its colon-separated path/mode/uid/gid parts,
// without splitting a drive letter's own colon out of the path.
func splitMkdirPathValue(value string) []string {
	vol := filepath.VolumeName(value)
	parts := strings.SplitN(value[len(vol):], ":", 4)
	parts[0] = vol + parts[0]
	return parts
}
