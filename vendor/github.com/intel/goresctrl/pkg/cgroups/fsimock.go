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

// This module implements a mock filesystem that can be used as a
// replacement for the native filesystem interface (fsi).

package cgroups

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type fsMock struct {
	files map[string]*mockFile // filesystem contents
}

type mockFile struct {
	// User-defined file properties
	data []byte // contents of the file

	// User/fsimock-defined properties
	info *mockFileInfo

	// File-specific user-overrides for the default file behavior
	open  func(string) (fileIface, error)
	read  func([]byte) (int, error)
	write func([]byte) (int, error)

	// fsimock-defined properties
	fs           *fsMock
	filename     string
	handle       *mockFileHandle
	writeHistory [][]byte
}

type mockFileHandle struct {
	pos int
}

type mockFileInfo struct {
	mode os.FileMode
	name string
	mf   *mockFile
}

func NewFsiMock(files map[string]mockFile) fsiIface {
	mfs := fsMock{}
	mfs.files = map[string]*mockFile{}
	for filename, usermf := range files {
		mf := usermf
		if mf.info == nil {
			mf.info = &mockFileInfo{}
		}
		if mf.info.name == "" {
			mf.info.name = filepath.Base(filename)
		}
		mf.filename = filename
		mf.info.mf = &mf
		mf.fs = &mfs
		mfs.files[filename] = &mf
	}
	return &mfs
}

func (mfs fsMock) OpenFile(name string, flag int, perm os.FileMode) (fileIface, error) {
	fsmockLog("OpenFile(%q, %d, %d)", name, flag, perm)
	if mf, ok := mfs.files[name]; ok {
		mf.handle = &mockFileHandle{}
		if mf.open != nil {
			return mf.open(name)
		}
		return *mf, nil
	}
	return nil, fsmockErrorf("%q: file not found", name)
}

func (mfs fsMock) Open(name string) (fileIface, error) {
	return mfs.OpenFile(name, 0, 0)
}

func (mfs fsMock) Walk(path string, walkFn filepath.WalkFunc) error {
	dirPath := strings.TrimSuffix(path, "/")
	info, err := mfs.Lstat(dirPath)
	if err != nil {
		err = walkFn(path, nil, err)
		return err
	}
	if !info.IsDir() {
		return walkFn(path, info, nil)
	}
	err = walkFn(path, info, nil)
	if err != nil {
		return err
	}
	for _, name := range mfs.dirContents(dirPath) {
		if err = mfs.Walk(dirPath+"/"+name, walkFn); err != nil && err != filepath.SkipDir {
			return err
		}
	}
	return nil
}

func (mfs fsMock) dirContents(path string) []string {
	dirPathS := strings.TrimSuffix(path, "/") + "/"
	contentSet := map[string]struct{}{}
	for filename := range mfs.files {
		if !strings.HasPrefix(filename, dirPathS) {
			continue
		}
		relToDirPath := strings.TrimPrefix(filename, dirPathS)
		names := strings.SplitN(relToDirPath, "/", 2)
		contentSet[names[0]] = struct{}{}
	}
	contents := make([]string, 0, len(contentSet))
	for name := range contentSet {
		contents = append(contents, name)
	}
	return contents
}

func (mfs fsMock) Lstat(path string) (os.FileInfo, error) {
	if mf, ok := mfs.files[path]; ok {
		return *mf.info, nil
	}
	if len(mfs.dirContents(path)) > 0 {
		return mockFileInfo{
			name: filepath.Base(path),
			mode: os.ModeDir,
		}, nil
	}
	return mockFileInfo{}, fsmockErrorf("%q: file not found", path)
}

func (mfi mockFileInfo) Name() string {
	return mfi.name
}
func (mfi mockFileInfo) Size() int64 {
	if mfi.mf != nil {
		return int64(len(mfi.mf.data))
	}
	return 0
}
func (mfi mockFileInfo) Mode() os.FileMode {
	return mfi.mode
}

func (mfi mockFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (mfi mockFileInfo) IsDir() bool {
	return mfi.mode&os.ModeDir != 0
}

func (mfi mockFileInfo) Sys() interface{} {
	return nil
}

func (mf mockFile) Write(b []byte) (n int, err error) {
	pos := mf.handle.pos
	if mf.write != nil {
		n, err = mf.write(b)
		if err == nil {
			mf.fs.files[mf.filename].writeHistory = append(mf.fs.files[mf.filename].writeHistory, b)
		}
	} else {
		newpos := pos + len(b)
		if newpos > cap(mf.data) {
			newdata := make([]byte, newpos)
			copy(newdata, mf.data)
			mf.data = newdata
		}
		copy(mf.data[pos:newpos], b)
		mf.handle.pos = newpos
		if f, ok := mf.fs.files[mf.filename]; ok {
			f.data = mf.data
		}
		mf.fs.files[mf.filename].writeHistory = append(mf.fs.files[mf.filename].writeHistory, b)
	}
	fsmockLog("{%q, pos=%d}.Write([%d]byte(%q)) = (%d, %v) %q", mf.filename, pos, len(b), string(b), n, err, mf.fs.files[mf.filename].data)
	return n, err
}

func (mf mockFile) Read(b []byte) (n int, err error) {
	pos := mf.handle.pos
	if mf.read != nil {
		n, err = mf.read(b)
	} else {
		n = len(mf.data) - pos
		err = nil
		if n <= 0 {
			err = io.EOF
		}
		if n > cap(b) {
			n = cap(b)
		}
		copy(b, mf.data[pos:pos+n])
		mf.handle.pos += n
	}
	fsmockLog("{%q, pos=%d}.Read([%d]byte) = (%d, %v)\n", mf.filename, pos, len(b), n, err)
	return
}

func (mf mockFile) Close() error {
	return nil
}

func fsmockLog(format string, args ...interface{}) {
	fmt.Printf("fsmock: "+format+"\n", args...)
}

func fsmockErrorf(format string, args ...interface{}) error {
	return fmt.Errorf("fsmock: "+format, args...)
}
