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

package metadata

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/filters"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
)

func snapshotLease(ctx context.Context, t *testing.T, db *DB, sn string) (context.Context, func(string) bool) {
	lm := NewLeaseManager(db)
	l, err := lm.Create(ctx, leases.WithRandomID())
	if err != nil {
		t.Fatal(err)
	}
	ltype := fmt.Sprintf("%s/%s", bucketKeyObjectSnapshots, sn)

	t.Cleanup(func() {
		lm.Delete(ctx, l)

	})
	return leases.WithLease(ctx, l.ID), func(id string) bool {
		resources, err := lm.ListResources(ctx, l)
		if err != nil {
			t.Error(err)
		}
		for _, r := range resources {
			if r.Type == ltype && r.ID == id {
				return true
			}
		}
		return false
	}
}

func TestSnapshotterWithRef(t *testing.T) {
	ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
		return NewTmpSnapshotter(), nil
	}))
	snapshotter := "tmp"
	ctx1, leased1 := snapshotLease(ctx, t, db, snapshotter)
	sn := db.Snapshotter(snapshotter)

	key1 := "test1"
	test1opt := snapshots.WithLabels(
		map[string]string{
			labelSnapshotRef: key1,
		},
	)

	key1t := "test1-tmp"
	_, err := sn.Prepare(ctx1, key1t, "", test1opt)
	if err != nil {
		t.Fatal(err)
	}
	if !leased1(key1t) {
		t.Errorf("no lease for %q", key1t)
	}

	err = sn.Commit(ctx1, key1, key1t, test1opt)
	if err != nil {
		t.Fatal(err)
	}
	if !leased1(key1) {
		t.Errorf("no lease for %q", key1)
	}
	if leased1(key1t) {
		t.Errorf("lease should be removed for %q", key1t)
	}

	ctx2 := namespaces.WithNamespace(ctx, "testing2")

	_, err = sn.Prepare(ctx2, key1t, "", test1opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	// test1 should now be in the namespace
	_, err = sn.Stat(ctx2, key1)
	if err != nil {
		t.Fatal(err)
	}

	key2t := "test2-tmp"
	key2 := "test2"
	test2opt := snapshots.WithLabels(
		map[string]string{
			labelSnapshotRef: key2,
		},
	)

	_, err = sn.Prepare(ctx2, key2t, key1, test2opt)
	if err != nil {
		t.Fatal(err)
	}

	// In original namespace, but not committed
	_, err = sn.Prepare(ctx1, key2t, key1, test2opt)
	if err != nil {
		t.Fatal(err)
	}
	if !leased1(key2t) {
		t.Errorf("no lease for %q", key2t)
	}
	if leased1(key2) {
		t.Errorf("lease for %q should not exist yet", key2)
	}

	err = sn.Commit(ctx2, key2, key2t, test2opt)
	if err != nil {
		t.Fatal(err)
	}

	// See note in Commit function for why
	// this does not return ErrAlreadyExists
	err = sn.Commit(ctx1, key2, key2t, test2opt)
	if err != nil {
		t.Fatal(err)
	}

	ctx2, leased2 := snapshotLease(ctx2, t, db, snapshotter)
	if leased2(key2) {
		t.Errorf("new lease should not have previously created snapshots")
	}
	// This should error out, already exists in namespace
	// despite mismatched parent
	key2ta := "test2-tmp-again"
	_, err = sn.Prepare(ctx2, key2ta, "", test2opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if !leased2(key2) {
		t.Errorf("no lease for %q", key2)
	}

	// In original namespace, but already exists
	_, err = sn.Prepare(ctx, key2ta, key1, test2opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if leased1(key2ta) {
		t.Errorf("should not have lease for non-existent snapshot %q", key2ta)
	}

	// Now try a third namespace

	ctx3 := namespaces.WithNamespace(ctx, "testing3")
	ctx3, leased3 := snapshotLease(ctx3, t, db, snapshotter)

	// This should error out, matching parent not found
	_, err = sn.Prepare(ctx3, key2t, "", test2opt)
	if err != nil {
		t.Fatal(err)
	}

	// Remove, not going to use yet
	err = sn.Remove(ctx3, key2t)
	if err != nil {
		t.Fatal(err)
	}

	_, err = sn.Prepare(ctx3, key2t, key1, test2opt)
	if err == nil {
		t.Fatal("expected not error")
	} else if !errdefs.IsNotFound(err) {
		t.Fatal(err)
	}
	if leased3(key1) {
		t.Errorf("lease for %q should not have been created", key1)
	}

	_, err = sn.Prepare(ctx3, key1t, "", test1opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if !leased3(key1) {
		t.Errorf("no lease for %q", key1)
	}

	_, err = sn.Prepare(ctx3, "test2-tmp", "test1", test2opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if !leased3(key2) {
		t.Errorf("no lease for %q", key2)
	}
}

func TestFilterInheritedLabels(t *testing.T) {
	tests := []struct {
		labels   map[string]string
		expected map[string]string
	}{
		{
			nil,
			nil,
		},
		{
			map[string]string{},
			map[string]string{},
		},
		{
			map[string]string{"": ""},
			map[string]string{},
		},
		{
			map[string]string{"foo": "bar"},
			map[string]string{},
		},
		{
			map[string]string{inheritedLabelsPrefix + "foo": "bar"},
			map[string]string{inheritedLabelsPrefix + "foo": "bar"},
		},
		{
			map[string]string{inheritedLabelsPrefix + "foo": "bar", "qux": "qaz"},
			map[string]string{inheritedLabelsPrefix + "foo": "bar"},
		},
	}

	for _, test := range tests {
		if actual := snapshots.FilterInheritedLabels(test.labels); !reflect.DeepEqual(actual, test.expected) {
			t.Fatalf("expected %v but got %v", test.expected, actual)
		}
	}
}

type tmpSnapshotter struct {
	l         sync.Mutex
	snapshots map[string]snapshots.Info
	targets   map[string][]string
}

func NewTmpSnapshotter() snapshots.Snapshotter {
	return &tmpSnapshotter{
		snapshots: map[string]snapshots.Info{},
		targets:   map[string][]string{},
	}
}

func (s *tmpSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	s.l.Lock()
	defer s.l.Unlock()
	i, ok := s.snapshots[key]
	if !ok {
		return snapshots.Info{}, errdefs.ErrNotFound
	}
	return i, nil
}

func (s *tmpSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	s.l.Lock()
	defer s.l.Unlock()

	i, ok := s.snapshots[info.Name]
	if !ok {
		return snapshots.Info{}, errdefs.ErrNotFound
	}

	for k, v := range info.Labels {
		i.Labels[k] = v
	}

	s.snapshots[i.Name] = i

	return i, nil
}

func (s *tmpSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	s.l.Lock()
	defer s.l.Unlock()
	_, ok := s.snapshots[key]
	if !ok {
		return snapshots.Usage{}, errdefs.ErrNotFound
	}
	return snapshots.Usage{}, nil
}

func (s *tmpSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	s.l.Lock()
	defer s.l.Unlock()
	_, ok := s.snapshots[key]
	if !ok {
		return nil, errdefs.ErrNotFound
	}
	return []mount.Mount{}, nil
}

func (s *tmpSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return s.create(ctx, key, parent, snapshots.KindActive, opts...)
}

func (s *tmpSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return s.create(ctx, key, parent, snapshots.KindView, opts...)
}

func (s *tmpSnapshotter) create(ctx context.Context, key, parent string, kind snapshots.Kind, opts ...snapshots.Opt) ([]mount.Mount, error) {
	s.l.Lock()
	defer s.l.Unlock()

	var base snapshots.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return nil, err
		}
	}
	base.Name = key
	base.Kind = kind

	target := base.Labels[labelSnapshotRef]
	if target != "" {
		for _, name := range s.targets[target] {
			if s.snapshots[name].Parent == parent {
				return nil, fmt.Errorf("found target: %w", errdefs.ErrAlreadyExists)
			}
		}
	}

	if parent != "" {
		_, ok := s.snapshots[parent]
		if !ok {
			return nil, errdefs.ErrNotFound
		}
		base.Parent = parent
	}

	ts := time.Now().UTC()
	base.Created = ts
	base.Updated = ts

	s.snapshots[base.Name] = base

	return []mount.Mount{}, nil
}

func (s *tmpSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	s.l.Lock()
	defer s.l.Unlock()

	var base snapshots.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return err
		}
	}
	base.Name = name
	base.Kind = snapshots.KindCommitted

	if _, ok := s.snapshots[name]; ok {
		return fmt.Errorf("found name: %w", errdefs.ErrAlreadyExists)
	}

	src, ok := s.snapshots[key]
	if !ok {
		return errdefs.ErrNotFound
	}
	if src.Kind == snapshots.KindCommitted {
		return errdefs.ErrInvalidArgument
	}
	base.Parent = src.Parent

	ts := time.Now().UTC()
	base.Created = ts
	base.Updated = ts

	s.snapshots[name] = base
	delete(s.snapshots, key)

	if target := base.Labels[labelSnapshotRef]; target != "" {
		s.targets[target] = append(s.targets[target], name)
	}

	return nil
}

func (s *tmpSnapshotter) Remove(ctx context.Context, key string) error {
	s.l.Lock()
	defer s.l.Unlock()

	sn, ok := s.snapshots[key]
	if !ok {
		return errdefs.ErrNotFound
	}
	delete(s.snapshots, key)

	// scan and remove all instances of name as a target
	for ref, names := range s.targets {
		for i := range names {
			if names[i] == sn.Name {
				if len(names) == 1 {
					delete(s.targets, ref)
				} else {
					copy(names[i:], names[i+1:])
					s.targets[ref] = names[:len(names)-1]
				}
				break
			}
		}
	}

	return nil
}

func (s *tmpSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	s.l.Lock()
	defer s.l.Unlock()

	filter, err := filters.ParseAll(fs...)
	if err != nil {
		return err
	}

	// call func for each
	for _, i := range s.snapshots {
		if filter.Match(adaptSnapshot(i)) {
			if err := fn(ctx, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *tmpSnapshotter) Close() error {
	return nil
}

// failingSnapshotter wraps a snapshotter to simulate remove failures
type failingSnapshotter struct {
	snapshots.Snapshotter
	failKeys map[string]bool // keys that should fail on Remove
}

func (f *failingSnapshotter) Remove(ctx context.Context, key string) error {
	if f.failKeys[key] {
		return fmt.Errorf("simulated remove failure for %s", key)
	}
	return f.Snapshotter.Remove(ctx, key)
}

// TestSnapshotGarbageCollectWithFailures tests that GC continues processing
// other snapshot trees even when some removals fail.
func TestSnapshotGarbageCollectWithFailures(t *testing.T) {
	t.Run("LeafNodeFailure", func(t *testing.T) {
		// Test that when leaf node L fails, only its ancestors (D, A, Root1) are blocked
		// but siblings (C, E) and other branches (B subtree, Root2) continue

		// Create a simpler mock scenario for unit testing
		ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
			return NewTmpSnapshotter(), nil
		}))

		sn := db.Snapshotter("tmp")

		// Create Root -> A -> L (will fail)
		//              -> B (should succeed)
		// Note: We create these without leases so they can be GC'd
		_, err := sn.Prepare(ctx, "Root-tmp", "")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Commit(ctx, "Root", "Root-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn.Prepare(ctx, "A-tmp", "Root")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Commit(ctx, "A", "A-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn.Prepare(ctx, "B-tmp", "Root")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Commit(ctx, "B", "B-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn.Prepare(ctx, "L-tmp", "A")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Commit(ctx, "L", "L-tmp")
		if err != nil {
			t.Fatal(err)
		}

		// Use metadata snapshotter's Remove to delete snapshots
		// This only removes metadata, not the backend snapshots
		// Making them unreferenced so GC will try to clean them up from backend
		err = sn.Remove(ctx, "L")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Remove(ctx, "B")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Remove(ctx, "A")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Remove(ctx, "Root")
		if err != nil {
			t.Fatal(err)
		}

		// Get the internal snapshotter and wrap it to fail on L's backend key
		ss := db.ss["tmp"]
		var lKey, aKey string
		ss.Snapshotter.Walk(ctx, func(ctx context.Context, info snapshots.Info) error {
			// Backend keys are like "testing/N/Name"
			// Extract the name part after the last slash
			parts := strings.Split(info.Name, "/")
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name == "A" {
					aKey = info.Name
				}
			}
			return nil
		})

		// Second pass to find L
		ss.Snapshotter.Walk(ctx, func(ctx context.Context, info snapshots.Info) error {
			if info.Parent == aKey {
				lKey = info.Name
			}
			return nil
		})

		if lKey == "" {
			t.Fatalf("Could not find L's backend key (aKey=%s)", aKey)
		}

		// Wrap snapshotter to fail on L
		failingSn := &failingSnapshotter{
			Snapshotter: ss.Snapshotter,
			failKeys:    map[string]bool{lKey: true},
		}
		ss.Snapshotter = failingSn

		// Run GC - All snapshots are unreferenced so should be removed
		// Except L (fails), A (blocked by L), Root (blocked by A)
		// B should be successfully removed
		d, err := ss.garbageCollect(ctx)
		t.Logf("GC duration: %v, error: %v", d, err)
		// GC should return error because some removals failed
		if err == nil {
			t.Error("Expected GC to return error when removals fail")
		}

		// Verify B was removed from backend (sibling of A should not be affected)
		var found_B bool
		ss.Snapshotter.Walk(ctx, func(ctx context.Context, info snapshots.Info) error {
			parts := strings.Split(info.Name, "/")
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name == "B" {
					found_B = true
				}
			}
			return nil
		})
		if found_B {
			t.Error("Expected B to be removed from backend")
		}

		// Verify L, A, Root still exist in backend (blocked by L's failure)
		var found_L, found_A, found_Root bool
		ss.Snapshotter.Walk(ctx, func(ctx context.Context, info snapshots.Info) error {
			parts := strings.Split(info.Name, "/")
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name == "L" {
					found_L = true
				} else if name == "A" {
					found_A = true
				} else if name == "Root" {
					found_Root = true
				}
			}
			return nil
		})

		if !found_L {
			t.Error("Expected L to still exist in backend (removal failed)")
		}
		if !found_A {
			t.Error("Expected A to still exist in backend (blocked by L)")
		}
		if !found_Root {
			t.Error("Expected Root to still exist in backend (blocked by A)")
		}
	})

	t.Run("MultipleRootFailures", func(t *testing.T) {
		// Test that failures in multiple root trees don't affect each other
		ctx3, db3 := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
			return NewTmpSnapshotter(), nil
		}))

		sn3 := db3.Snapshotter("tmp")

		// Create Root1 -> A (will fail)
		//        Root2 -> B (should succeed)
		_, err := sn3.Prepare(ctx3, "Root1-tmp", "")
		if err != nil {
			t.Fatal(err)
		}
		err = sn3.Commit(ctx3, "Root1", "Root1-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn3.Prepare(ctx3, "A-tmp", "Root1")
		if err != nil {
			t.Fatal(err)
		}
		err = sn3.Commit(ctx3, "A", "A-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn3.Prepare(ctx3, "Root2-tmp", "")
		if err != nil {
			t.Fatal(err)
		}
		err = sn3.Commit(ctx3, "Root2", "Root2-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn3.Prepare(ctx3, "B-tmp", "Root2")
		if err != nil {
			t.Fatal(err)
		}
		err = sn3.Commit(ctx3, "B", "B-tmp")
		if err != nil {
			t.Fatal(err)
		}

		// Use metadata snapshotter's Remove to delete snapshots
		// This only removes metadata, not the backend snapshots
		// Making them unreferenced so GC will try to clean them up from backend
		err = sn3.Remove(ctx3, "B")
		if err != nil {
			t.Fatal(err)
		}
		err = sn3.Remove(ctx3, "Root2")
		if err != nil {
			t.Fatal(err)
		}
		err = sn3.Remove(ctx3, "A")
		if err != nil {
			t.Fatal(err)
		}
		err = sn3.Remove(ctx3, "Root1")
		if err != nil {
			t.Fatal(err)
		}

		// Make A fail
		ss3 := db3.ss["tmp"]
		var root1Key, aKey string
		// First pass: find Root1
		ss3.Snapshotter.Walk(ctx3, func(ctx context.Context, info snapshots.Info) error {
			parts := strings.Split(info.Name, "/")
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name == "Root1" {
					root1Key = info.Name
				}
			}
			return nil
		})

		// Second pass: find A
		ss3.Snapshotter.Walk(ctx3, func(ctx context.Context, info snapshots.Info) error {
			parts := strings.Split(info.Name, "/")
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name == "A" && info.Parent == root1Key {
					aKey = info.Name
				}
			}
			return nil
		})

		if aKey == "" {
			t.Fatalf("Could not find A's backend key (root1Key=%s)", root1Key)
		}

		failingSn3 := &failingSnapshotter{
			Snapshotter: ss3.Snapshotter,
			failKeys:    map[string]bool{aKey: true},
		}
		ss3.Snapshotter = failingSn3

		// Run GC
		_, err = ss3.garbageCollect(ctx3)
		if err == nil {
			t.Error("Expected GC to return error when removals fail")
		}

		// Verify Root2 tree was completely removed from backend (not affected by Root1's failure)
		var found_B, found_Root2 bool
		ss3.Snapshotter.Walk(ctx3, func(ctx context.Context, info snapshots.Info) error {
			parts := strings.Split(info.Name, "/")
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name == "B" {
					found_B = true
				} else if name == "Root2" {
					found_Root2 = true
				}
			}
			return nil
		})

		if found_B {
			t.Error("Expected B to be removed from backend")
		}
		if found_Root2 {
			t.Error("Expected Root2 to be removed from backend")
		}

		// Verify Root1 tree still exists in backend
		var found_A, found_Root1 bool
		ss3.Snapshotter.Walk(ctx3, func(ctx context.Context, info snapshots.Info) error {
			parts := strings.Split(info.Name, "/")
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name == "A" {
					found_A = true
				} else if name == "Root1" {
					found_Root1 = true
				}
			}
			return nil
		})

		if !found_A {
			t.Error("Expected A to still exist in backend (removal failed)")
		}
		if !found_Root1 {
			t.Error("Expected Root1 to still exist in backend (blocked by A)")
		}
	})

	t.Run("ErrorsContainSnapshotIDs", func(t *testing.T) {
		// Test that errors contain snapshot IDs for debugging
		ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
			return NewTmpSnapshotter(), nil
		}))

		sn := db.Snapshotter("tmp")

		// Create Root1 -> A and Root2 -> B
		_, err := sn.Prepare(ctx, "Root1-tmp", "")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Commit(ctx, "Root1", "Root1-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn.Prepare(ctx, "A-tmp", "Root1")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Commit(ctx, "A", "A-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn.Prepare(ctx, "Root2-tmp", "")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Commit(ctx, "Root2", "Root2-tmp")
		if err != nil {
			t.Fatal(err)
		}

		_, err = sn.Prepare(ctx, "B-tmp", "Root2")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Commit(ctx, "B", "B-tmp")
		if err != nil {
			t.Fatal(err)
		}

		// Remove metadata to trigger GC
		err = sn.Remove(ctx, "A")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Remove(ctx, "Root1")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Remove(ctx, "B")
		if err != nil {
			t.Fatal(err)
		}
		err = sn.Remove(ctx, "Root2")
		if err != nil {
			t.Fatal(err)
		}

		// Get the internal snapshotter and make both A and B fail
		ss := db.ss["tmp"]
		var aKey, bKey string
		ss.Snapshotter.Walk(ctx, func(ctx context.Context, info snapshots.Info) error {
			parts := strings.Split(info.Name, "/")
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if name == "A" {
					aKey = info.Name
				} else if name == "B" {
					bKey = info.Name
				}
			}
			return nil
		})

		if aKey == "" || bKey == "" {
			t.Fatalf("Could not find snapshot keys (aKey=%s, bKey=%s)", aKey, bKey)
		}

		failingSn := &failingSnapshotter{
			Snapshotter: ss.Snapshotter,
			failKeys:    map[string]bool{aKey: true, bKey: true},
		}
		ss.Snapshotter = failingSn

		_, err = ss.garbageCollect(ctx)
		if err == nil {
			t.Fatal("Expected GC to return error")
		}

		errMsg := err.Error()
		if !strings.Contains(errMsg, "A") {
			t.Errorf("Expected error message to contain snapshot ID 'A', got: %v", errMsg)
		}
		if !strings.Contains(errMsg, "B") {
			t.Errorf("Expected error message to contain snapshot ID 'B', got: %v", errMsg)
		}
	})
}
