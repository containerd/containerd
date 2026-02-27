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

package v2

import (
	"os"
	"path/filepath"
)

// createWorkLink creates a symlink from bundlePath/work -> work.
func createWorkLink(work, bundlePath string) error {
	return os.Symlink(work, filepath.Join(bundlePath, "work"))
}

// resolveWorkLink reads the symlink at bundlePath/work to recover the
// working directory path.
func resolveWorkLink(bundlePath string) (string, error) {
	return os.Readlink(filepath.Join(bundlePath, "work"))
}
