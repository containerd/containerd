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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateWorkLinkJunction(t *testing.T) {
	dir := t.TempDir()

	work := filepath.Join(dir, "root", "default", "test-id")
	bundlePath := filepath.Join(dir, "state", "default", "test-id")

	require.NoError(t, os.MkdirAll(work, 0711), "failed to create work dir")
	require.NoError(t, os.MkdirAll(bundlePath, 0700), "failed to create bundle dir")

	// Create the junction
	require.NoError(t, createWorkLink(work, bundlePath), "failed to create work link")

	// Verify the junction target resolves back to work
	resolved, err := resolveWorkLink(bundlePath)
	require.NoError(t, err, "failed to resolve work link")

	absWork, err := filepath.Abs(work)
	require.NoError(t, err, "failed to get absolute path")
	assert.Equal(t, absWork, resolved, "resolved work link should match absolute work path")

	// Write a file through the junction and read from the target
	testContent := []byte("hello")
	require.NoError(t, os.WriteFile(filepath.Join(bundlePath, "work", "testfile"), testContent, 0644), "failed to write through junction")

	got, err := os.ReadFile(filepath.Join(work, "testfile"))
	require.NoError(t, err, "failed to read from work dir")
	assert.Equal(t, testContent, got, "content written through junction should be readable from target")
}
