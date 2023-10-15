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

package images

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd/errdefs"
	snapshotstore "github.com/containerd/containerd/pkg/cri/store/snapshot"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	snapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/log"
)

// snapshotsSyncer syncs snapshot stats periodically. imagefs info and container stats
// should both use cached result here.
// TODO(random-liu): Benchmark with high workload. We may need a statsSyncer instead if
// benchmark result shows that container cpu/memory stats also need to be cached.
type snapshotsSyncer struct {
	store        *snapshotstore.Store
	snapshotters map[string]snapshot.Snapshotter
	syncPeriod   time.Duration
}

// newSnapshotsSyncer creates a snapshot syncer.
func newSnapshotsSyncer(store *snapshotstore.Store, snapshotters map[string]snapshot.Snapshotter,
	period time.Duration) *snapshotsSyncer {
	return &snapshotsSyncer{
		store:        store,
		snapshotters: snapshotters,
		syncPeriod:   period,
	}
}

// start starts the snapshots syncer. No stop function is needed because
// the syncer doesn't update any persistent states, it's fine to let it
// exit with the process.
func (s *snapshotsSyncer) start() {
	tick := time.NewTicker(s.syncPeriod)
	go func() {
		defer tick.Stop()
		// TODO(random-liu): This is expensive. We should do benchmark to
		// check the resource usage and optimize this.
		for {
			if err := s.sync(); err != nil {
				log.L.WithError(err).Error("Failed to sync snapshot stats")
			}
			<-tick.C
		}
	}()
}

// sync updates all snapshots stats.
func (s *snapshotsSyncer) sync() error {
	ctx := ctrdutil.NamespacedContext()
	start := time.Now().UnixNano()

	for key, snapshotter := range s.snapshotters {
		var snapshots []snapshot.Info
		// Do not call `Usage` directly in collect function, because
		// `Usage` takes time, we don't want `Walk` to hold read lock
		// of snapshot metadata store for too long time.
		// TODO(random-liu): Set timeout for the following 2 contexts.
		if err := snapshotter.Walk(ctx, func(ctx context.Context, info snapshot.Info) error {
			snapshots = append(snapshots, info)
			return nil
		}); err != nil {
			return fmt.Errorf("walk all snapshots for %q failed: %w", key, err)
		}
		for _, info := range snapshots {
			snapshotKey := snapshotstore.Key{
				Key:         info.Name,
				Snapshotter: key,
			}
			sn, err := s.store.Get(snapshotKey)
			if err == nil {
				// Only update timestamp for non-active snapshot.
				if sn.Kind == info.Kind && sn.Kind != snapshot.KindActive {
					sn.Timestamp = time.Now().UnixNano()
					s.store.Add(sn)
					continue
				}
			}
			// Get newest stats if the snapshot is new or active.
			sn = snapshotstore.Snapshot{
				Key: snapshotstore.Key{
					Key:         info.Name,
					Snapshotter: key,
				},
				Kind:      info.Kind,
				Timestamp: time.Now().UnixNano(),
			}
			usage, err := snapshotter.Usage(ctx, info.Name)
			if err != nil {
				if !errdefs.IsNotFound(err) {
					log.L.WithError(err).Errorf("Failed to get usage for snapshot %q", info.Name)
				}
				continue
			}
			sn.Size = uint64(usage.Size)
			sn.Inodes = uint64(usage.Inodes)
			s.store.Add(sn)
		}
	}

	for _, sn := range s.store.List() {
		if sn.Timestamp >= start {
			continue
		}
		// Delete the snapshot stats if it's not updated this time.
		s.store.Delete(sn.Key)
	}
	return nil
}
