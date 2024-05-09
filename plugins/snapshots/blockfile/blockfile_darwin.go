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

package blockfile

import "github.com/containerd/continuity/fs"

func copyFileWithSync(target, source string) error {
	// The Go stdlib does not seem to have an efficient os.File.ReadFrom
	// routine for other platforms like it does on Linux with
	// copy_file_range. For Darwin at least we can use clonefile
	// in its place.
	return fs.CopyFile(target, source)
}
