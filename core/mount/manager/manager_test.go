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

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"

	bolt "go.etcd.io/bbolt"
)

func TestManager(t *testing.T) {
	testutil.RequiresRoot(t)
	td := t.TempDir()
	metadb := filepath.Join(td, "mounts.db")
	targetdir := filepath.Join(td, "m")
	db, err := bolt.Open(metadb, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := namespaces.WithNamespace(context.Background(), "test")

	sourcedir := filepath.Join(td, "source")
	if err := os.Mkdir(sourcedir, 0700); err != nil {
		t.Fatal(err)
	}
	mounts := []mount.Mount{
		{
			Type:    "bind",
			Source:  sourcedir,
			Options: []string{"rbind", "ro"},
		},
	}

	t.Run("ActivateNoMounts", func(t *testing.T) {
		handlers := map[string]mount.MountHandler{}
		_, err = NewManager(db, targetdir, handlers).Activate(ctx, "id1", []mount.Mount{})
		assert.ErrorIs(t, err, errdefs.ErrNotImplemented)
	})

	t.Run("SystemOnly", func(t *testing.T) {
		handlers := map[string]mount.MountHandler{}
		_, err = NewManager(db, targetdir, handlers).Activate(ctx, "id1", mounts)
		assert.ErrorIs(t, err, errdefs.ErrNotImplemented)
	})

	t.Run("SystemOverride", func(t *testing.T) {
		handlers := map[string]mount.MountHandler{
			"bind": nil,
		}
		m := NewManager(db, targetdir, handlers)
		ainfo, err := m.Activate(ctx, "id1", mounts)
		require.NoError(t, err)
		defer assert.NoError(t, m.Deactivate(ctx, "id1"))

		assert.Equal(t, len(ainfo.Active), 1)
		assert.Equal(t, len(ainfo.System), 0)
		assert.Equal(t, ainfo.Active[0].Source, sourcedir)
		assert.Equal(t, ainfo.Active[0].Type, "bind")
		assert.Equal(t, ainfo.Active[0].MountPoint, filepath.Join(targetdir, "1", "1"))
	})

	// try mounting
	// Test mount

}

// TODO: Test formatting
// TODO: Test Info
// TODO: Test deactivate
// TODO: Test all custom
// TODO: Test GC
// TODO: Test Sync
