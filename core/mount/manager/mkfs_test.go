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
	"runtime"
	"strings"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureMkfsImageExistingDirectory verifies that ensureMkfsImage
// rejects an existing directory at the backing file path.
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

// TestEnsureMkfsImageExistingRegularFile verifies that an existing
// regular file is accepted as an already-formatted image.
func TestEnsureMkfsImageExistingRegularFile(t *testing.T) {
	td := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(td, "existing"), []byte("not really a filesystem"), 0600))

	r, err := os.OpenRoot(td)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	require.NoError(t, ensureMkfsImage(context.Background(), r, "existing", "existing", 0, "ext4", ""))
}

// TestEnsureMkfsImagePassesAbsolutePath verifies that the path handed
// to the mkfs.* subprocess is always absolute, even when the
// configured root is relative.
func TestEnsureMkfsImagePassesAbsolutePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on a #!/bin/sh fake mkfs.ext4, not runnable on Windows")
	}

	td := t.TempDir()
	root := filepath.Join(td, "root")
	require.NoError(t, os.MkdirAll(root, 0700))

	wd, err := os.Getwd()
	require.NoError(t, err)
	relRoot, err := filepath.Rel(wd, root)
	require.NoError(t, err)

	r, err := os.OpenRoot(relRoot)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	argsFile := filepath.Join(td, "args")
	bin := filepath.Join(td, "bin")
	require.NoError(t, os.MkdirAll(bin, 0700))
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "mkfs.ext4"), []byte(script), 0700))
	t.Setenv("PATH", bin)

	require.NoError(t, ensureMkfsImage(context.Background(), r, "img", "img", 4096, "ext4", ""))

	recorded, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	fields := strings.Fields(string(recorded))
	require.NotEmpty(t, fields)
	imgPath := fields[len(fields)-1]
	assert.True(t, filepath.IsAbs(imgPath), "path passed to mkfs.ext4 must be absolute, got %q", imgPath)
	assert.Equal(t, filepath.Join(root, "img"), imgPath)
}
