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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListXattrsErrors verifies that a failure to list xattrs is returned
// rather than read as "no xattrs": losing trusted.overlay.opaque would expose
// lower-layer entries.
func TestListXattrsErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(file, nil, 0644))
	names, err := listXattrs(file)
	require.NoError(t, err)
	assert.Empty(t, names)

	_, err = listXattrs(filepath.Join(t.TempDir(), "missing"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// TestApplyXattrsCanceled verifies that the xattr pass stops on a canceled
// context.
func TestApplyXattrsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, applyXattrs(ctx, nil, t.TempDir()), context.Canceled)
}
