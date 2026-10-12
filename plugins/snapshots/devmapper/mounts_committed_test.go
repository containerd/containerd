//go:build linux

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

package devmapper

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
)

func TestMountsCommittedSnapshotReturnsError(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewMetaStore(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	s := &Snapshotter{store: store, config: &Config{PoolName: "pool"}}

	err = store.WithTransaction(ctx, true, func(ctx context.Context) error {
		if _, err := storage.CreateSnapshot(ctx, snapshots.KindActive, "active", "",
			snapshots.WithLabels(map[string]string{devmapperSnapshotFsType: string(fsTypeExt4)})); err != nil {
			return err
		}
		_, err := storage.CommitActive(ctx, "active", "committed", snapshots.Usage{})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	mounts, err := s.Mounts(ctx, "committed")
	if !errdefs.IsFailedPrecondition(err) {
		t.Fatalf("expected a failed precondition error for a committed snapshot, got err=%v mounts=%+v", err, mounts)
	}
}
