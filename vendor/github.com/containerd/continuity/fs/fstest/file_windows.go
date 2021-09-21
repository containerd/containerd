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

package fstest

import (
	"errors"
	"time"
)

// Lchtimes changes access and mod time of file without following symlink
func Lchtimes(name string, atime, mtime time.Time) Applier {
	return applyFn(func(root string) error {
		return errors.New("Not implemented")
	})
}

// Base applies the files required to make a valid Windows container layer
// that the filter will mount. It is used for testing the snapshotter
func Base() Applier {
	return Apply(
		CreateDir("Windows", 0755),
		CreateDir("Windows/System32", 0755),
		CreateDir("Windows/System32/Config", 0755),
		CreateFile("Windows/System32/Config/SYSTEM", []byte("foo\n"), 0777),
		CreateFile("Windows/System32/Config/SOFTWARE", []byte("foo\n"), 0777),
		CreateFile("Windows/System32/Config/SAM", []byte("foo\n"), 0777),
		CreateFile("Windows/System32/Config/SECURITY", []byte("foo\n"), 0777),
		CreateFile("Windows/System32/Config/DEFAULT", []byte("foo\n"), 0777),
	)
}
