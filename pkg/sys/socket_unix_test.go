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

package sys

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMkdirAsPermissionErrorIncludesHint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test requires a non-root user to observe a permission error")
	}

	parent := t.TempDir()
	require.NoError(t, os.Chmod(parent, 0500))

	err := mkdirAs(filepath.Join(parent, "child"), os.Geteuid(), os.Getegid())
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrPermission))
	assert.Contains(t, err.Error(), "non-root user")
}

func TestMkdirAsStatPermissionErrorIncludesHint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test requires a non-root user to observe a permission error")
	}

	parent := t.TempDir()
	target := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(target, 0770))
	require.NoError(t, os.Chmod(parent, 0000))
	t.Cleanup(func() { _ = os.Chmod(parent, 0700) })

	err := mkdirAs(target, os.Geteuid(), os.Getegid())
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrPermission))
	assert.Contains(t, err.Error(), "non-root user")
}
