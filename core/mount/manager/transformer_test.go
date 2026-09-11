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
	"runtime"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestRoot opens root as an *os.Root, creating it first, and
// registers it to close when the test ends.
func openTestRoot(t *testing.T, root string) *os.Root {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0700))
	r, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	return r
}

// TestResolveRootPathBoundary verifies that a root only matches a
// path under it, not one which merely shares a textual prefix.
func TestResolveRootPathBoundary(t *testing.T) {
	td := t.TempDir()
	base := filepath.Join(td, "base")
	r := openTestRoot(t, base)
	roots := map[string]*os.Root{base: r}

	_, _, err := resolveRoot(roots, base+"2/img.raw", "test")
	assert.True(t, errdefs.IsNotImplemented(err),
		"a path sharing only a textual prefix with the root must not match, got %v", err)

	root, subpath, err := resolveRoot(roots, filepath.Join(base, "img.raw"), "test")
	require.NoError(t, err)
	assert.Same(t, r, root)
	assert.Equal(t, "img.raw", subpath)

	root, subpath, err = resolveRoot(roots, base, "test")
	require.NoError(t, err)
	assert.Same(t, r, root)
	assert.Equal(t, ".", subpath)
}

// TestResolveRootLongestMatch verifies that a path under two nested
// roots resolves against the longer, more specific one.
func TestResolveRootLongestMatch(t *testing.T) {
	td := t.TempDir()
	outer := filepath.Join(td, "outer")
	inner := filepath.Join(outer, "inner")
	rOuter := openTestRoot(t, outer)
	rInner := openTestRoot(t, inner)

	roots := map[string]*os.Root{
		outer: rOuter,
		inner: rInner,
	}

	dir := filepath.Join(inner, "x")
	// Repeated since map iteration order is randomized per range.
	for range 20 {
		root, subpath, err := resolveRoot(roots, dir, "test")
		require.NoError(t, err)
		assert.Same(t, rInner, root, "the longer, more specific root must win")
		assert.Equal(t, "x", subpath)
	}
}

// TestResolveRootNoMatch verifies that a path under no configured
// root is reported as errdefs.ErrNotImplemented.
func TestResolveRootNoMatch(t *testing.T) {
	td := t.TempDir()
	base := filepath.Join(td, "base")
	r := openTestRoot(t, base)
	roots := map[string]*os.Root{base: r}

	_, _, err := resolveRoot(roots, filepath.Join(td, "elsewhere"), "mkfs")
	assert.True(t, errdefs.IsNotImplemented(err), "expected ErrNotImplemented, got %v", err)
	assert.ErrorContains(t, err, "mkfs")
}

// TestResolveRootTrailingSeparator verifies that a configured root
// with a trailing separator still matches.
func TestResolveRootTrailingSeparator(t *testing.T) {
	td := t.TempDir()
	base := filepath.Join(td, "base")
	r := openTestRoot(t, base)
	roots := map[string]*os.Root{base + string(filepath.Separator): r}

	root, subpath, err := resolveRoot(roots, filepath.Join(base, "img.raw"), "test")
	require.NoError(t, err)
	assert.Same(t, r, root)
	assert.Equal(t, "img.raw", subpath)
}

// TestResolveRootIsFilesystemRoot verifies that a configured root
// which is the root of the whole filesystem matches and resolves a
// correct subpath.
func TestResolveRootIsFilesystemRoot(t *testing.T) {
	fsRoot := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		fsRoot = `C:\`
	}
	r, err := os.OpenRoot(fsRoot)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	roots := map[string]*os.Root{fsRoot: r}

	dir := filepath.Join(fsRoot, "a", "b")
	root, subpath, err := resolveRoot(roots, dir, "test")
	require.NoError(t, err)
	assert.Same(t, r, root)
	assert.Equal(t, filepath.Join("a", "b"), subpath)

	root, subpath, err = resolveRoot(roots, fsRoot, "test")
	require.NoError(t, err)
	assert.Same(t, r, root)
	assert.Equal(t, ".", subpath)
}

// TestResolveRootTrailingSeparatorMatchesRootItself verifies that a
// configured root with a trailing separator matches dir when dir is
// that same root spelled without one.
func TestResolveRootTrailingSeparatorMatchesRootItself(t *testing.T) {
	td := t.TempDir()
	base := filepath.Join(td, "base")
	r := openTestRoot(t, base)
	roots := map[string]*os.Root{base + string(filepath.Separator): r}

	root, subpath, err := resolveRoot(roots, base, "test")
	require.NoError(t, err)
	assert.Same(t, r, root)
	assert.Equal(t, ".", subpath)
}
