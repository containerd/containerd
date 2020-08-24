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

package mount

import (
	"os"

	"github.com/pkg/errors"
)

var (
	// ErrNotImplementOnUnix is returned for methods that are not implemented
	ErrNotImplementOnUnix = errors.New("not implemented under darwin")
)

// Mount to the provided target path.
//
// Use symlink instead of union mount on darwin
func (m *Mount) Mount(target string) error {
	if m.Type != "bind" {
		return errors.Errorf("invalid mount type: '%s'", m.Type)
	}

	if err := os.Remove(target); err != nil {
		return err
	}
	if err := os.Symlink(m.Source, target); err != nil {
		return err
	}

	return nil
}

// Unmount is just a rm of symlink
func Unmount(mount string, flags int) error {
	os.RemoveAll(mount)
	return os.Mkdir(mount, 0755)
}

// UnmountAll is just a rm of symlink
func UnmountAll(mount string, flags int) error {
	os.RemoveAll(mount)
	return os.Mkdir(mount, 0755)
}
