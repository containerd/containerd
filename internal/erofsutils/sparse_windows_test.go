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

package erofsutils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

const fileAttributeSparseFile = 0x00000200 // FILE_ATTRIBUTE_SPARSE_FILE

// TestEnsureSparseLayerFile creates the file and verifies the sparse
// attribute is set.
func TestEnsureSparseLayerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layer.erofs")

	require.NoError(t, ensureSparseLayerFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Zero(t, info.Size())

	require.True(t, fileHasSparseAttr(t, path))
}

// TestEnsureSparseLayerFile_Idempotent verifies repeated calls don't
// error and preserve the sparse flag.
func TestEnsureSparseLayerFile_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layer.erofs")

	require.NoError(t, ensureSparseLayerFile(path))
	require.NoError(t, ensureSparseLayerFile(path))

	require.True(t, fileHasSparseAttr(t, path))
}

func fileHasSparseAttr(t *testing.T, path string) bool {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	require.NoError(t, err)
	attrs, err := syscall.GetFileAttributes(p)
	require.NoError(t, err)
	return attrs&fileAttributeSparseFile != 0
}
