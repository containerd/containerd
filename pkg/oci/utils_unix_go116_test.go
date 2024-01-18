//go:build !go1.17 && !windows && !darwin

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

package oci

import "io/fs"

// The following code is adapted from go1.17.1/src/io/fs/readdir.go
// to compensate for the lack of fs.FileInfoToDirEntry in Go 1.16.

// dirInfo is a DirEntry based on a FileInfo.
type dirInfo struct {
	fileInfo fs.FileInfo
}

func (di dirInfo) IsDir() bool {
	return di.fileInfo.IsDir()
}

func (di dirInfo) Type() fs.FileMode {
	return di.fileInfo.Mode().Type()
}

func (di dirInfo) Info() (fs.FileInfo, error) {
	return di.fileInfo, nil
}

func (di dirInfo) Name() string {
	return di.fileInfo.Name()
}

// fileInfoToDirEntry returns a DirEntry that returns information from info.
// If info is nil, FileInfoToDirEntry returns nil.
func fileInfoToDirEntry(info fs.FileInfo) fs.DirEntry {
	if info == nil {
		return nil
	}
	return dirInfo{fileInfo: info}
}
