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
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/filters"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	bolt "go.etcd.io/bbolt"
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
			snapshots.LabelSnapshotRef: key1,
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
			snapshots.LabelSnapshotRef: key2,
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

	maps.Copy(i.Labels, info.Labels)

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

	target := base.Labels[snapshots.LabelSnapshotRef]
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

	if target := base.Labels[snapshots.LabelSnapshotRef]; target != "" {
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

type hangingRemoveSnapshotter struct {
	snapshots.Snapshotter
	removes  atomic.Int32
	cleanups atomic.Int32
}

func (s *hangingRemoveSnapshotter) Remove(ctx context.Context, key string) error {
	s.removes.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

// Cleanup makes the wrapper a snapshots.Cleaner so the test can assert GC does
// not call it after abandoning a pass. This stub returns immediately; a real
// wedged snapshotter would hang here just like it does in Remove.
func (s *hangingRemoveSnapshotter) Cleanup(ctx context.Context) error {
	s.cleanups.Add(1)
	return nil
}

func TestGarbageCollectHangingRemove(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, bdb := testEnv(t)

		sn := &hangingRemoveSnapshotter{Snapshotter: NewTmpSnapshotter()}
		db := NewDB(bdb, nil, map[string]snapshots.Snapshotter{"tmp": sn})
		if err := db.Init(ctx); err != nil {
			t.Fatal(err)
		}

		// Orphan snapshots: exist in the backend but not in metadata, so GC removes them.
		for _, key := range []string{"orphan-1", "orphan-2"} {
			if _, err := sn.Snapshotter.Prepare(ctx, key, ""); err != nil {
				t.Fatal(err)
			}
		}

		// The fake clock reaches the removal deadline as soon as Remove blocks,
		// so this runs against the real default instead of a shortened one. An
		// unbounded Remove would deadlock the bubble rather than pass.
		msn := db.Snapshotter("tmp").(*snapshotter)
		_, err := msn.garbageCollect(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected the bounded Remove to fail with DeadlineExceeded, got %v", err)
		}

		// The pass is abandoned after the first timed-out removal.
		if n := sn.removes.Load(); n != 1 {
			t.Fatalf("expected 1 remove attempt before abandoning the pass, got %d", n)
		}
		// Cleanup must be skipped after an abandoned pass: the snapshotter just
		// failed to answer a bounded Remove, and Cleanup has no bound.
		if n := sn.cleanups.Load(); n != 0 {
			t.Fatalf("expected no Cleanup call after abandoned pass, got %d", n)
		}
		// Skipped snapshots stay orphaned for the next GC pass.
		for _, key := range []string{"orphan-1", "orphan-2"} {
			if _, err := sn.Snapshotter.Stat(ctx, key); err != nil {
				t.Fatalf("orphan %q should survive for the next GC pass: %v", key, err)
			}
		}
	})
}

type failingRemoveSnapshotter struct {
	snapshots.Snapshotter
}

func (s *failingRemoveSnapshotter) Remove(ctx context.Context, key string) error {
	return errdefs.ErrFailedPrecondition
}

func TestGarbageCollectFailedPreconditionRemove(t *testing.T) {
	ctx, bdb := testEnv(t)

	sn := &failingRemoveSnapshotter{Snapshotter: NewTmpSnapshotter()}
	db := NewDB(bdb, nil, map[string]snapshots.Snapshotter{"tmp": sn})
	if err := db.Init(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := sn.Snapshotter.Prepare(ctx, "orphan", ""); err != nil {
		t.Fatal(err)
	}

	msn := db.Snapshotter("tmp").(*snapshotter)
	if _, err := msn.garbageCollect(ctx); err != nil {
		t.Fatalf("FailedPrecondition on Remove must not fail garbage collection: %v", err)
	}
}

// childrenOf reads a snapshot's children bucket directly. The bug this file
// guards is invisible through the public API -- Walk cannot see a stranded
// child, and Remove only reports that one exists -- so the assertions have to
// look at the stored link itself.
func childrenOf(t *testing.T, db *DB, ns, snapshotter, key string) []string {
	t.Helper()
	var out []string
	if err := db.View(func(tx *bolt.Tx) error {
		bkt := getSnapshotterBucket(tx, ns, snapshotter)
		if bkt == nil {
			return nil
		}
		sbkt := bkt.Bucket([]byte(key))
		if sbkt == nil {
			return nil
		}
		cbkt := sbkt.Bucket(bucketKeyChildren)
		if cbkt == nil {
			return nil
		}
		return cbkt.ForEach(func(k, _ []byte) error {
			out = append(out, string(k))
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func commitSnapshot(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, key, parent string) {
	t.Helper()
	if _, err := sn.Prepare(ctx, key+"-active", parent); err != nil {
		t.Fatalf("prepare %s: %v", key, err)
	}
	if err := sn.Commit(ctx, key, key+"-active"); err != nil {
		t.Fatalf("commit %s: %v", key, err)
	}
}

func walkKeys(ctx context.Context, t *testing.T, sn snapshots.Snapshotter) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	if err := sn.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		seen[info.Name] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return seen
}

// A snapshot removed by the garbage collector must be unlinked from its parent,
// or the parent becomes permanently unremovable: Remove refuses any snapshot
// whose children bucket is non-empty, and Walk -- which enumerates snapshot
// buckets -- cannot see a child whose bucket is already gone. The parent is then
// stuck behind a blocker nothing can observe. See containerd#11908.
func TestGarbageCollectedSnapshotUnlinksFromParent(t *testing.T) {
	ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
		return NewTmpSnapshotter(), nil
	}))
	sn := db.Snapshotter("tmp")

	// base is leased so it survives collection; child is not, so the GC takes
	// it. Both must hold, or the Remove below proves nothing.
	leasedCtx, _ := snapshotLease(ctx, t, db, "tmp")
	commitSnapshot(leasedCtx, t, sn, "base", "")
	commitSnapshot(ctx, t, sn, "child", "base")

	if got := childrenOf(t, db, "testing", "tmp", "base"); len(got) != 1 {
		t.Fatalf("expected base to have one child before collection, got %v", got)
	}

	if _, err := db.GarbageCollect(ctx); err != nil {
		t.Fatal(err)
	}

	seen := walkKeys(ctx, t, sn)
	if seen["child"] {
		t.Fatal("child survived collection; this test cannot exercise the bug")
	}
	if !seen["base"] {
		t.Fatal("base was collected too; this test cannot exercise the bug")
	}

	// The stored link, not just the symptom: a stranded entry here is what
	// makes base unremovable forever.
	if got := childrenOf(t, db, "testing", "tmp", "base"); len(got) != 0 {
		t.Errorf("base still links to collected children: %v", got)
	}
	if err := sn.Remove(ctx, "base"); err != nil {
		t.Fatalf("base unremovable after its child was garbage collected: %v", err)
	}
}

// The real shape: an image's layer chain, where only the tip is collected.
func TestGarbageCollectedSnapshotUnlinksThroughAChain(t *testing.T) {
	ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
		return NewTmpSnapshotter(), nil
	}))
	sn := db.Snapshotter("tmp")

	leasedCtx, _ := snapshotLease(ctx, t, db, "tmp")
	commitSnapshot(leasedCtx, t, sn, "base", "")
	commitSnapshot(leasedCtx, t, sn, "mid", "base")
	commitSnapshot(ctx, t, sn, "leaf", "mid")

	if _, err := db.GarbageCollect(ctx); err != nil {
		t.Fatal(err)
	}

	seen := walkKeys(ctx, t, sn)
	if seen["leaf"] || !seen["mid"] || !seen["base"] {
		t.Fatalf("unexpected survivors after collection: %v", seen)
	}
	if got := childrenOf(t, db, "testing", "tmp", "mid"); len(got) != 0 {
		t.Errorf("mid still links to the collected leaf: %v", got)
	}
	// Leaf-first, exactly as a caller emptying a snapshotter must do.
	if err := sn.Remove(ctx, "mid"); err != nil {
		t.Fatalf("mid unremovable: %v", err)
	}
	if err := sn.Remove(ctx, "base"); err != nil {
		t.Fatalf("base unremovable: %v", err)
	}
}

// A parent with one collected and one surviving child must still refuse
// removal -- for the surviving child, which is a real blocker, not a stale
// link. Unlinking must be precise, not a wipe of the children bucket.
func TestGarbageCollectedSnapshotLeavesSurvivingSiblingsLinked(t *testing.T) {
	ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
		return NewTmpSnapshotter(), nil
	}))
	sn := db.Snapshotter("tmp")

	leasedCtx, _ := snapshotLease(ctx, t, db, "tmp")
	commitSnapshot(leasedCtx, t, sn, "base", "")
	commitSnapshot(leasedCtx, t, sn, "keeper", "base")
	commitSnapshot(ctx, t, sn, "doomed", "base")

	if _, err := db.GarbageCollect(ctx); err != nil {
		t.Fatal(err)
	}

	seen := walkKeys(ctx, t, sn)
	if seen["doomed"] || !seen["keeper"] {
		t.Fatalf("unexpected survivors: %v", seen)
	}
	children := childrenOf(t, db, "testing", "tmp", "base")
	if len(children) != 1 || children[0] != "keeper" {
		t.Fatalf("base children = %v, want exactly [keeper]", children)
	}
	if err := sn.Remove(ctx, "base"); err == nil {
		t.Fatal("base removed while a live child still exists")
	}
	// ...and once the survivor goes, the parent is free.
	if err := sn.Remove(ctx, "keeper"); err != nil {
		t.Fatal(err)
	}
	if err := sn.Remove(ctx, "base"); err != nil {
		t.Fatalf("base unremovable after all children are gone: %v", err)
	}
}

// Parent and child collected together: the unlink must not care which bucket
// bolt deletes first.
func TestGarbageCollectingParentAndChildTogetherIsClean(t *testing.T) {
	ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
		return NewTmpSnapshotter(), nil
	}))
	sn := db.Snapshotter("tmp")

	commitSnapshot(ctx, t, sn, "base", "")
	commitSnapshot(ctx, t, sn, "child", "base")

	if _, err := db.GarbageCollect(ctx); err != nil {
		t.Fatal(err)
	}
	if seen := walkKeys(ctx, t, sn); len(seen) != 0 {
		t.Fatalf("expected an empty snapshotter, got %v", seen)
	}
}

// A root snapshot has no parent to unlink from; collecting it must not error.
func TestGarbageCollectedRootSnapshotNeedsNoUnlink(t *testing.T) {
	ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
		return NewTmpSnapshotter(), nil
	}))
	sn := db.Snapshotter("tmp")

	commitSnapshot(ctx, t, sn, "solo", "")

	if _, err := db.GarbageCollect(ctx); err != nil {
		t.Fatalf("collecting a parentless snapshot failed: %v", err)
	}
	if seen := walkKeys(ctx, t, sn); seen["solo"] {
		t.Fatal("solo survived collection")
	}
}

// Regression guard: the explicit Remove path already unlinked, and must keep
// doing so. This fix is about the collector matching it, not replacing it.
func TestExplicitRemoveStillUnlinksFromParent(t *testing.T) {
	ctx, db := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
		return NewTmpSnapshotter(), nil
	}))
	sn := db.Snapshotter("tmp")

	leasedCtx, _ := snapshotLease(ctx, t, db, "tmp")
	commitSnapshot(leasedCtx, t, sn, "base", "")
	commitSnapshot(leasedCtx, t, sn, "child", "base")

	if err := sn.Remove(ctx, "child"); err != nil {
		t.Fatal(err)
	}
	if got := childrenOf(t, db, "testing", "tmp", "base"); len(got) != 0 {
		t.Errorf("explicit Remove left a stale child link: %v", got)
	}
	if err := sn.Remove(ctx, "base"); err != nil {
		t.Fatalf("base unremovable after its child was explicitly removed: %v", err)
	}
}
