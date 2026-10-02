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

package v2

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	// When testutil is imported for one platform (bundle_linux_test.go) it
	// should be imported for all platforms.
	_ "github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewBundleSelfHealsExistingPath verifies that NewBundle recovers when a
// previous task's bundle state directory is left behind on disk. On Windows,
// atomicDelete's os.Rename can fail while a still-exiting shim holds file
// handles on bundle files, leaving the directory in place; the next NewTask
// must not fail with "file already exists".
func TestNewBundleSelfHealsExistingPath(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	state := filepath.Join(dir, "state")
	const id = "self-heal"

	ctx := namespaces.WithNamespace(context.Background(), namespaces.Default)

	// Simulate a leftover bundle directory from a previous run, including a
	// stale file inside it to prove RemoveAll runs.
	stalePath := filepath.Join(state, namespaces.Default, id)
	require.NoError(t, os.MkdirAll(stalePath, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(stalePath, "stale"), []byte("x"), 0600))

	b, err := NewBundle(ctx, root, state, id, nil)
	require.NoError(t, err, "NewBundle should self-heal a pre-existing bundle path")
	require.NotNil(t, b)

	_, err = os.Stat(filepath.Join(b.Path, "stale"))
	assert.True(t, os.IsNotExist(err), "stale file should have been cleared by self-heal")
}
