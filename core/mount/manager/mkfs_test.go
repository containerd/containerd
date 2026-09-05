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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureMkfsImageExistingDirectory verifies that ensureMkfsImage
// reports a clear error when the backing file it is asked to bring
// into existence already exists but is a directory, rather than
// silently accepting it as an already-formatted image: mounting a
// directory as a loop-backed filesystem would otherwise fail later,
// with an error that no longer points back to this being why. This
// exercises only the early return on an existing path, not mkfs
// itself, so it needs neither root nor an mkfs binary.
func TestEnsureMkfsImageExistingDirectory(t *testing.T) {
	td := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(td, "existing"), 0700))

	r, err := os.OpenRoot(td)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	err = ensureMkfsImage(context.Background(), r, "existing", "existing", 0, "ext4", "")
	require.Error(t, err)
	assert.True(t, errdefs.IsFailedPrecondition(err), "expected ErrFailedPrecondition, got %v", err)
}

// TestEnsureMkfsImageExistingRegularFile verifies the paired positive
// case: an existing regular file is still accepted as an
// already-formatted image and left untouched, exactly as before this
// check was added.
func TestEnsureMkfsImageExistingRegularFile(t *testing.T) {
	td := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(td, "existing"), []byte("not really a filesystem"), 0600))

	r, err := os.OpenRoot(td)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	require.NoError(t, ensureMkfsImage(context.Background(), r, "existing", "existing", 0, "ext4", ""))
}
