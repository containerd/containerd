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

package testsuite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/mount/manager"
)

type mountManagerKey struct{}

func withMountManager(ctx context.Context, t testing.TB, mopts ...manager.Opt) context.Context {
	root := t.TempDir()

	targets := filepath.Join(root, "t")
	if err := os.MkdirAll(targets, 0700); err != nil {
		t.Fatalf("failed to create targets directory: %v", err)
	}

	metadb := filepath.Join(root, "mounts.db")

	db, err := bolt.Open(metadb, 0600, nil)
	if err != nil {
		t.Fatalf("failed to open database file: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close boltdb: %v", err)
		}
	})

	mm, err := manager.NewManager(db, targets, mopts...)
	if err != nil {
		t.Fatalf("failed to create mount manager: %v", err)
	}
	t.Cleanup(func() { mm.(io.Closer).Close() })

	return context.WithValue(ctx, mountManagerKey{}, mm)
}

func mountAll(ctx context.Context, m []mount.Mount, td string) error {
	contextValue := ctx.Value(mountManagerKey{})
	if mm, ok := contextValue.(mount.Manager); ok {
		// TODO: Activation option for temp
		ai, err := mm.Activate(ctx, td, m)
		if err == nil {
			m = ai.System
		} else if !errors.Is(err, errdefs.ErrNotImplemented) {
			return fmt.Errorf("failed to activate mounts: %v", err)
		}
	}

	if err := mount.All(m, td); err != nil {
		return fmt.Errorf("failed to mount: %w", err)
	}
	return nil
}

const umountflags int = 0

func unmountCtx(ctx context.Context, td string) error {
	err := mount.UnmountAll(td, umountflags)
	if err != nil {
		return err
	}
	contextValue := ctx.Value(mountManagerKey{})
	if mm, ok := contextValue.(mount.Manager); ok {
		if err := mm.Deactivate(ctx, td); err != nil && !errors.Is(err, errdefs.ErrNotFound) {
			return fmt.Errorf("failed to deactivate mounts: %v", err)
		}
	}
	return nil
}

func unmountAll(ctx context.Context, t testing.TB, td string) {
	t.Helper()
	t.Log("unmount", td)
	err := unmountCtx(ctx, td)
	if err != nil {
		t.Fatalf("failed to unmount %s: %v", td, err)
	}
}
