/*
Copyright 2017 The Kubernetes Authors.

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
	"os"
)

// OS collects system level operations that need to be mocked out
// during tests.
type OS interface {
	MkdirAll(path string, perm os.FileMode) error
	RemoveAll(path string) error
}

// RealOS is used to dispatch the real system level operations.
type RealOS struct{}

// MkdirAll will will call os.MkdirAll to create a directory.
func (RealOS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// RemoveAll will call os.RemoveAll to remove the path and its children.
func (RealOS) RemoveAll(path string) error {
	return os.RemoveAll(path)
}
