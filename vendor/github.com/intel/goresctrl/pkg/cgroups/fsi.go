// Copyright 2021 Intel Corporation. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This module defines filesystem interface (fsi) through which
// cgroups package accesses files.

package cgroups

import (
	"os"
	"path/filepath"
)

type fsiIface interface {
	Open(name string) (fileIface, error)
	OpenFile(name string, flag int, perm os.FileMode) (fileIface, error)
	Walk(string, filepath.WalkFunc) error
	Lstat(path string) (os.FileInfo, error)
}

type fileIface interface {
	Close() error
	Read(p []byte) (n int, err error)
	Write(b []byte) (n int, err error)
}

// Set the default filesystem interface
var fsi fsiIface = newFsiOS()
