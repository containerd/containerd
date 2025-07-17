//go:build linux

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
	"context"
	"os"
	"path/filepath"
	"time"
)

// Applier is an interface for applying filesystem changes.
type Applier interface {
	Apply(root string) error
}

type applyFn func(root string) error

func (fn applyFn) Apply(root string) error {
	return fn(root)
}

/*
   --------------------------------------------------------------------
   Ownership helpers
   --------------------------------------------------------------------
*/

// ChownFunc is used by Chown() to change ownership.  
// Default: os.Chown.  
// Tests can overwrite this variable to inject deterministic errors
// (e.g. return unix.EBUSY on the first N calls) so that retry logic
// can be verified without root privileges or special kernel modules.
var ChownFunc = os.Chown

// Chown changes uid/gid of the given path and retries on transient
// failures such as EBUSY / EPERM.
//
// Retry policy: 5 attempts, linear 10 ms back-off.
func Chown(path string, uid, gid int) Applier {
	return applyFn(func(root string) error {
		abs := filepath.Join(root, path)
		return Retry(context.Background(), 5, 10*time.Millisecond, func() error {
			return ChownFunc(abs, uid, gid)
		})
	})
}

/*
   --------------------------------------------------------------------
   Generic helpers
   --------------------------------------------------------------------
*/

// CreateFile creates a file with the given content.
func CreateFile(path string, content []byte, mode os.FileMode) Applier {
	return applyFn(func(root string) error {
		return os.WriteFile(filepath.Join(root, path), content, mode)
	})
}

// Apply executes a series of Appliers in order.
func Apply(appliers ...Applier) Applier {
	return applyFn(func(root string) error {
		for _, a := range appliers {
			if err := a.Apply(root); err != nil {
				return err
			}
		}
		return nil
	})
}
