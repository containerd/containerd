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

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/moby/sys/user"
	"github.com/stretchr/testify/assert"
)

// TestOpenUserFileCapsReads asserts the boundary behavior of the read cap:
// well below, ending exactly at, and past maxUserFileBytes.
//
// Regression test for CVE-2026-47262 / GHSA-jpcc-p29g-p8mq
func TestOpenUserFileCapsReads(t *testing.T) {
	t.Parallel()

	beyond := []byte("\nbeyond:x:42:\n")

	for _, tc := range []struct {
		name     string
		padBytes int
		wantGids []uint32
		wantErr  bool
	}{
		{
			name:     "pad below cap, beyond is parsed",
			padBytes: 100,
			wantGids: []uint32{42},
		},
		{
			name:     "beyond ends exactly at cap, is parsed",
			padBytes: maxUserFileBytes - len(beyond),
			wantGids: []uint32{42},
		},
		{
			name:     "pad past cap, read errors out",
			padBytes: maxUserFileBytes,
			wantErr:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pattern := []byte("# padding\n")
			pad := bytes.Repeat(pattern, (tc.padBytes+len(pattern)-1)/len(pattern))[:tc.padBytes]
			if len(pad) > 0 {
				pad[len(pad)-1] = '\n'
			}

			data := append(pad, beyond...)
			fsys := fstest.MapFS{
				"etc/group": &fstest.MapFile{Data: data, Mode: 0o644},
			}

			gids, err := getSupplementalGroupsFromFS(fsys, func(g user.Group) bool {
				return g.Name == "beyond"
			})
			if tc.wantErr {
				assert.ErrorContains(t, err, "exceeds")
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantGids, gids)
		})
	}
}

// TestOpenUserFileRejectsNonRegularFiles verifies that non-regular files
// are refused before any byte is read from them.
func TestOpenUserFileRejectsNonRegularFiles(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mode fs.FileMode
	}{
		{name: "char device", mode: fs.ModeDevice | fs.ModeCharDevice | 0o666},
		{name: "socket", mode: fs.ModeSocket | 0o666},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &nonRegularFile{mode: tc.mode}
			rootFS := singleFileFS{name: "etc/group", file: f}

			_, err := getSupplementalGroupsFromFS(rootFS, nil)
			assert.Error(t, err)
			assert.False(t, f.readCalled, "Read should not be called on non-regular file")
		})
	}
}

// nonRegularFile implements fs.File and reports a configurable non-regular
// mode via Stat.
type nonRegularFile struct {
	mode       fs.FileMode
	readCalled bool
}

func (f *nonRegularFile) Read([]byte) (int, error) {
	f.readCalled = true
	return 0, errors.New("read should not be called on non-regular file")
}

func (f *nonRegularFile) Stat() (fs.FileInfo, error) {
	return nonRegularFileInfo{mode: f.mode}, nil
}
func (f *nonRegularFile) Close() error { return nil }

type nonRegularFileInfo struct {
	mode fs.FileMode
}

func (nonRegularFileInfo) Name() string        { return "group" }
func (nonRegularFileInfo) Size() int64         { return 0 }
func (i nonRegularFileInfo) Mode() fs.FileMode { return i.mode }
func (nonRegularFileInfo) ModTime() time.Time  { return time.Time{} }
func (nonRegularFileInfo) IsDir() bool         { return false }
func (nonRegularFileInfo) Sys() any            { return nil }

// singleFileFS routes a single name to a single fs.File and returns
// fs.ErrNotExist for everything else.
type singleFileFS struct {
	name string
	file fs.File
}

func (s singleFileFS) Open(name string) (fs.File, error) {
	if name == s.name {
		return s.file, nil
	}
	return nil, fs.ErrNotExist
}
