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

package snapshot

import (
	"sync"

	snapshot "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/errdefs"
)

type Key struct {
	// Key is the key of the snapshot
	Key string
	// Snapshotter is the name of the snapshotter managing the snapshot
	Snapshotter string
}

// Snapshot contains the information about the snapshot.
type Snapshot struct {
	// Key is the key of the snapshot
	Key Key
	// Kind is the kind of the snapshot (active, committed, view)
	Kind snapshot.Kind
	// Size is the size of the snapshot in bytes.
	Size uint64
	// Inodes is the number of inodes used by the snapshot
	Inodes uint64
	// Timestamp is latest update time (in nanoseconds) of the snapshot
	// information.
	Timestamp int64
}

// Store stores all snapshots and snapshotter metadata.
type Store struct {
	lock      sync.RWMutex
	snapshots map[Key]Snapshot
	// snapshotters contains all snapshotters indexed by name.
	snapshotters map[string]snapshot.Snapshotter
	// snapshotterExports contains exports metadata for each snapshotter.
	snapshotterExports map[string]map[string]string
}

// NewStore creates a snapshot store.
func NewStore(snapshotters map[string]snapshot.Snapshotter, exports map[string]map[string]string) *Store {
	return &Store{
		snapshots:          make(map[Key]Snapshot),
		snapshotters:       snapshotters,
		snapshotterExports: exports,
	}
}

// Snapshotters returns all snapshotters.
func (s *Store) Snapshotters() map[string]snapshot.Snapshotter {
	return s.snapshotters
}

// GetSnapshotter returns the snapshotter with specified name.
func (s *Store) GetSnapshotter(name string) (snapshot.Snapshotter, bool) {
	sn, ok := s.snapshotters[name]
	return sn, ok
}

// IsRemoteSnapshotter returns true if the snapshotter is a remote snapshotter
// (like stargz or nydus) that supports lazy loading.
// Remote snapshotters are identified by the "enable_remote_snapshot_annotations" export.
func (s *Store) IsRemoteSnapshotter(name string) bool {
	if exports, ok := s.snapshotterExports[name]; ok {
		if v, exists := exports["enable_remote_snapshot_annotations"]; exists && v == "true" {
			return true
		}
	}
	return false
}

// Add a snapshot into the store.
func (s *Store) Add(snapshot Snapshot) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.snapshots[snapshot.Key] = snapshot
}

// Get returns the snapshot with specified key. Returns errdefs.ErrNotFound if the
// snapshot doesn't exist.
func (s *Store) Get(key Key) (Snapshot, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if sn, ok := s.snapshots[key]; ok {
		return sn, nil
	}
	return Snapshot{}, errdefs.ErrNotFound
}

// List lists all snapshots.
func (s *Store) List() []Snapshot {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var snapshots []Snapshot
	for _, sn := range s.snapshots {
		snapshots = append(snapshots, sn)
	}
	return snapshots
}

// Delete deletes the snapshot with specified key.
func (s *Store) Delete(key Key) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.snapshots, key)
}
