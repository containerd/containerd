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

package os

import (
	"errors"

	"github.com/containerd/containerd/mount"
)

// Mount is an empty stub on Windows.
func (RealOS) Mount(source string, target string, fstype string, flags uintptr, data string) error {
	return errors.New("mount is not supported on Windows")
}

// Unmount is an empty stub on Windows.
func (RealOS) Unmount(target string) error {
	return errors.New("unmount is not supported on Windows")
}

// LookupMount is an empty stub on Windows.
func (RealOS) LookupMount(path string) (mount.Info, error) {
	return mount.Info{}, errors.New("mount lookups are not supported on Windows")
}
