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

	snapshot "github.com/containerd/containerd/v2/core/snapshots"
	snapshotstore "github.com/containerd/containerd/v2/internal/cri/store/snapshot"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
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
	// Set minimum sleep to half of syncPeriod to ensure adequate CPU release
	minSleep := s.syncPeriod / 2
	// Use EMA to smooth sleep adjustments and avoid abrupt changes between iterations.
	emaAlpha := 0.2
	targetSleep := float64(s.syncPeriod)
	go func() {
		// TODO(random-liu): This is expensive. We should do benchmark to
		// check the resource usage and optimize this.
		for {
			begin := time.Now()
			if err := s.sync(); err != nil {
				log.L.WithError(err).Error("Failed to sync snapshot stats")
			}
			cost := time.Since(begin)
			desiredSleep := float64(s.syncPeriod - cost)
			targetSleep = calculateEMA(targetSleep, desiredSleep, emaAlpha)
			// Ensure minimum CPU release even if EMA converges too low.
			sleepDuration := max(time.Duration(targetSleep), minSleep)

			time.Sleep(sleepDuration)
		}
	}()
}

func calculateEMA(previous, sample, alpha float64) float64 {
	return previous + alpha*(sample-previous)
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
