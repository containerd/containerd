//go:build !windows

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
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/moby/sys/user"
	"github.com/stretchr/testify/assert"
)

// TestOpenBoundedUserFileCapsReads asserts the boundary behavior of the read
// cap: well below, ending exactly at, and past maxUserFileBytes.
func TestOpenBoundedUserFileCapsReads(t *testing.T) {
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

			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
				t.Fatal(err)
			}
			data := append(bytes.Repeat([]byte{0}, tc.padBytes), beyond...)
			if err := os.WriteFile(filepath.Join(root, "etc", "group"), data, 0o644); err != nil {
				t.Fatal(err)
			}

			gids, err := getSupplementalGroupsFromPath(root, func(g user.Group) bool {
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

// TestOpenBoundedUserFileRejectsNonRegularFiles verifies that non-regular
// files are refused before any byte is read from them.
func TestOpenBoundedUserFileRejectsNonRegularFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "etc", "group"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := getSupplementalGroupsFromPath(root, nil)
	assert.ErrorContains(t, err, "not a regular file")
}
