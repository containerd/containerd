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

package manager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestSetSparseFile(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "sparse.img"))
	require.NoError(t, err)
	defer f.Close()

	require.NoError(t, setSparseFile(f), "failed to set sparse attribute")

	// mkfs extends the file with Truncate right after this; the attribute
	// must still be set afterwards.
	require.NoError(t, f.Truncate(1<<30), "failed to extend file")

	var info windows.ByHandleFileInformation
	require.NoError(t, windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info))
	assert.NotZero(t, info.FileAttributes&windows.FILE_ATTRIBUTE_SPARSE_FILE, "file should have the sparse attribute")
}
