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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/gc"
	"github.com/containerd/containerd/v2/pkg/namespaces"

	bolt "go.etcd.io/bbolt"
)

// v1Active is one position in a v1 activation's mount chain, in the
// shape that schema recorded it: type and mount point only, since
// source, target and options were never implemented there.
type v1Active struct {
	typ string
	mp  string
	at  time.Time
}

// v1Activation describes one activation to seed under the "v1" bucket
// name. A nil active slice produces an activation with no active
// bucket at all, matching one interrupted before completion.
type v1Activation struct {
	name   string
	lease  string
	labels map[string]string
	active []v1Active
}

// seedV1 writes activations directly under the bucket name of the
// schema this package replaced, exactly as a binary which predates
// this schema would have. It exists only to build fixtures for tests
// in this file: production code never writes this bucket.
func seedV1(t *testing.T, db boltDB, namespace string, activations ...v1Activation) {
	t.Helper()
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		oldbkt, err := tx.CreateBucketIfNotExists([]byte("v1"))
		if err != nil {
			return err
		}
		nsbkt, err := oldbkt.CreateBucketIfNotExists([]byte(namespace))
		if err != nil {
			return err
		}
		mbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyMounts)
		if err != nil {
			return err
		}

		var lsbkt *bolt.Bucket
		for i, a := range activations {
			bkt, err := mbkt.CreateBucket([]byte(a.name))
			if err != nil {
				return err
			}
			idb, err := encodeID(uint64(i + 1))
			if err != nil {
				return err
			}
			if err := bkt.Put(bucketKeyID, idb); err != nil {
				return err
			}

			if a.lease != "" {
				if err := bkt.Put(bucketKeyLease, []byte(a.lease)); err != nil {
					return err
				}
				if lsbkt == nil {
					lsbkt, err = nsbkt.CreateBucketIfNotExists(bucketKeyLeases)
					if err != nil {
						return err
					}
				}
				lbkt, err := lsbkt.CreateBucketIfNotExists([]byte(a.lease))
				if err != nil {
					return err
				}
				if err := lbkt.Put([]byte(a.name), nil); err != nil {
					return err
				}
			}

			if len(a.labels) > 0 {
				lblbkt, err := bkt.CreateBucket(bucketKeyLabels)
				if err != nil {
					return err
				}
				for k, v := range a.labels {
					if err := lblbkt.Put([]byte(k), []byte(v)); err != nil {
						return err
					}
				}
			}

			if a.active != nil {
				abkt, err := bkt.CreateBucket(bucketKeyActive)
				if err != nil {
					return err
				}
				for j, act := range a.active {
					cur, err := abkt.CreateBucket([]byte{byte(j)})
					if err != nil {
						return err
					}
					if err := cur.Put(bucketKeyType, []byte(act.typ)); err != nil {
						return err
					}
					if err := cur.Put(bucketKeyMountPoint, []byte(act.mp)); err != nil {
						return err
					}
					atb, err := act.at.MarshalBinary()
					if err != nil {
						return err
					}
					if err := cur.Put(bucketKeyMountedAt, atb); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}))
}

// assertV1Present asserts whether the bucket name of the schema this
// package replaced is still present in db.
func assertV1Present(t *testing.T, db boltDB, present bool, msgAndArgs ...interface{}) {
	t.Helper()
	require.NoError(t, db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket([]byte("v1"))
		if present {
			assert.NotNil(t, bkt, msgAndArgs...)
		} else {
			assert.Nil(t, bkt, msgAndArgs...)
		}
		return nil
	}))
}

// TestV1Info verifies that a v1 activation is readable through Info
// exactly as it was recorded: v1 never implemented Source or Options,
// so neither is ever populated for one, unlike a v2 activation.
func TestV1Info(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: new(atomic.Int32)}))
	mm := m.(*mountManager)

	at := time.Now().Truncate(time.Second)
	seedV1(t, mm.db, "test", v1Activation{
		name: "old",
		labels: map[string]string{
			"containerd.io/gc.bref.container": "c1",
			"custom-label":                    "value1",
		},
		active: []v1Active{
			{typ: "noop", mp: "/legacy/lower", at: at},
			{typ: "noop", mp: "/legacy/upper", at: at},
		},
	})

	info, err := m.Info(ctx, "old")
	require.NoError(t, err)
	assert.Equal(t, "old", info.Name)
	require.Len(t, info.Active, 2)
	assert.Equal(t, "noop", info.Active[0].Type)
	assert.Equal(t, "/legacy/lower", info.Active[0].MountPoint)
	assert.Empty(t, info.Active[0].Source, "v1 never recorded source")
	assert.Empty(t, info.Active[0].Options, "v1 never recorded options")
	require.NotNil(t, info.Active[0].MountedAt)
	assert.Equal(t, at.Unix(), info.Active[0].MountedAt.Unix())
	assert.Equal(t, "noop", info.Active[1].Type)
	assert.Equal(t, "/legacy/upper", info.Active[1].MountPoint)
	assert.Equal(t, "c1", info.Labels["containerd.io/gc.bref.container"])
	assert.Equal(t, "value1", info.Labels["custom-label"])
	assert.Empty(t, info.System, "v1 never recorded system mounts separately")

	// Reading it must not have touched v1 at all.
	assertV1Present(t, mm.db, true)
}

// TestV1InfoIncomplete verifies that a v1 activation interrupted
// before it completed, which never created an active bucket, is
// reported through Info with an empty Active list rather than an
// error, exactly as v1's own native Info always reported one.
func TestV1InfoIncomplete(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t)
	mm := m.(*mountManager)

	seedV1(t, mm.db, "test", v1Activation{name: "gone", active: nil})

	info, err := m.Info(ctx, "gone")
	require.NoError(t, err)
	assert.Equal(t, "gone", info.Name)
	assert.Empty(t, info.Active)
}

// TestV1List verifies that List reports activations from both
// schemas together.
func TestV1List(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: new(atomic.Int32)}))
	mm := m.(*mountManager)

	seedV1(t, mm.db, "test", v1Activation{
		name:   "old",
		active: []v1Active{{typ: "noop", mp: "/legacy/old", at: time.Now()}},
	})

	_, err := m.Activate(ctx, "new", []mount.Mount{{Type: "noop", Source: "/dev/null"}})
	require.NoError(t, err)

	infos, err := m.List(ctx)
	require.NoError(t, err)
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name
	}
	assert.ElementsMatch(t, []string{"old", "new"}, names)

	require.NoError(t, m.Deactivate(ctx, "new"))
}

// TestV1CollisionPrecedence verifies that a name which somehow exists
// in both schemas at once, reachable only via a rollback to a v1
// binary and forward again, is resolved consistently in favor of the
// v2 activation by both Info and List, without disturbing the v1
// namesake: they are unrelated resources which only happen to share a
// name.
func TestV1CollisionPrecedence(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	mm := m.(*mountManager)

	ainfo, err := m.Activate(ctx, "a", []mount.Mount{{Type: "noop", Source: "/dev/null"}})
	require.NoError(t, err)
	liveMP := ainfo.Active[0].MountPoint

	seedV1(t, mm.db, "test", v1Activation{
		name:   "a",
		active: []v1Active{{typ: "noop", mp: "/legacy/a", at: time.Now()}},
	})

	info, err := m.Info(ctx, "a")
	require.NoError(t, err)
	require.Len(t, info.Active, 1)
	assert.Equal(t, liveMP, info.Active[0].MountPoint, "Info must prefer the v2 activation")

	infos, err := m.List(ctx)
	require.NoError(t, err)
	require.Len(t, infos, 1, "List must not report the v1 namesake alongside the v2 activation")
	assert.Equal(t, liveMP, infos[0].Active[0].MountPoint)

	// The v1 namesake is untouched: Activate never looked at it, since
	// a live v2 activation by this name already existed.
	assertV1Present(t, mm.db, true)

	require.NoError(t, m.Deactivate(ctx, "a"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestV1Deactivate verifies that Deactivate releases a v1 activation:
// it unmounts its recorded chain and removes its target directory,
// the same as it always did before this schema existed.
func TestV1Deactivate(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	unmounts := new(atomic.Int32)
	handler := &noopHandler{mounts: mountC, unmountAttempts: unmounts}
	m, targetdir := mkTestManager(t, WithMountHandler("noop", handler))
	mm := m.(*mountManager)

	target := filepath.Join(targetdir, "1")
	require.NoError(t, os.MkdirAll(target, 0700))

	seedV1(t, mm.db, "test", v1Activation{
		name: "old",
		active: []v1Active{
			{typ: "noop", mp: filepath.Join(target, "0"), at: time.Now()},
			{typ: "noop", mp: filepath.Join(target, "1"), at: time.Now()},
		},
	})

	require.NoError(t, m.Deactivate(ctx, "old"))

	assert.Equal(t, int32(2), unmounts.Load(), "unmount attempted once per position")
	assert.Equal(t, int32(0), mountC.Load(), "the fixture was never actually mounted through the handler")
	_, err := os.Stat(target)
	assert.True(t, os.IsNotExist(err), "the v1 activation's target directory should be removed")

	_, err = m.Info(ctx, "old")
	assert.True(t, errdefs.IsNotFound(err), "expected ErrNotFound, got %v", err)
	assertV1Present(t, mm.db, true, "the v1 bucket itself, now with no activations left in it, is left in place")
}

// TestV1DeactivateNotFound verifies that Deactivate reports
// ErrNotFound for a name which exists in neither schema.
func TestV1DeactivateNotFound(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t)

	err := m.Deactivate(ctx, "nonexistent")
	assert.True(t, errdefs.IsNotFound(err), "expected ErrNotFound, got %v", err)
}

// TestActivateReportsLiveV1AlreadyExists verifies that Activate
// reports ErrAlreadyExists, without touching anything, for a name
// whose v1 activation is still actually mounted.
func TestActivateReportsLiveV1AlreadyExists(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	handler := &noopHandler{mounts: mountC}
	m, _ := mkTestManager(t, WithMountHandler("noop", handler))
	mm := m.(*mountManager)

	mp := "/legacy/live"
	seedV1(t, mm.db, "test", v1Activation{
		name:   "old",
		active: []v1Active{{typ: "noop", mp: mp, at: time.Now()}},
	})
	// Simulate the v1 mount still actually being in effect.
	handler.live = map[string]struct{}{mp: {}}

	_, err := m.Activate(ctx, "old", []mount.Mount{{Type: "noop", Source: "/dev/null"}})
	assert.True(t, errdefs.IsAlreadyExists(err), "expected ErrAlreadyExists, got: %v", err)
	assertV1Present(t, mm.db, true)

	info, err := m.Info(ctx, "old")
	require.NoError(t, err)
	assert.Equal(t, mp, info.Active[0].MountPoint, "the v1 activation must be untouched")
}

// TestActivateReplacesDeadV1 verifies that Activate treats a v1
// activation whose recorded mount is no longer actually mounted as
// stale, releasing and unmounting it before creating a fresh v2
// activation under the same name.
func TestActivateReplacesDeadV1(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	handler := &noopHandler{mounts: mountC}
	m, targetdir := mkTestManager(t, WithMountHandler("noop", handler))
	mm := m.(*mountManager)

	target := filepath.Join(targetdir, "1")
	require.NoError(t, os.MkdirAll(target, 0700))
	mp := filepath.Join(target, "0")

	seedV1(t, mm.db, "test", v1Activation{
		name:   "old",
		active: []v1Active{{typ: "noop", mp: mp, at: time.Now()}},
	})
	// Not marked live on the handler: this position is not actually
	// mounted, matching wreckage left behind by a pre-upgrade crash.

	ainfo, err := m.Activate(ctx, "old", []mount.Mount{{Type: "noop", Source: "/dev/null"}})
	require.NoError(t, err)
	require.Len(t, ainfo.Active, 1)
	assert.Equal(t, int32(1), mountC.Load(), "a fresh v2 mount was made")

	_, err = os.Stat(target)
	assert.True(t, os.IsNotExist(err), "the dead v1 activation's target directory should be removed")
	assertV1Present(t, mm.db, true, "the v1 bucket itself is left in place, now with this activation released from it")

	require.NoError(t, m.Deactivate(ctx, "old"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestActivateReplacesIncompleteV1 verifies that a v1 activation
// interrupted before it completed, which never created an active
// bucket, is treated as stale unconditionally, without needing
// anything to probe, exactly as an equivalent v2 activation would be.
func TestActivateReplacesIncompleteV1(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	mm := m.(*mountManager)

	seedV1(t, mm.db, "test", v1Activation{name: "gone", active: nil})

	ainfo, err := m.Activate(ctx, "gone", []mount.Mount{{Type: "noop", Source: "/dev/null"}})
	require.NoError(t, err)
	require.Len(t, ainfo.Active, 1)

	require.NoError(t, m.Deactivate(ctx, "gone"))
	assert.Equal(t, int32(0), mountC.Load())
}

// TestV1GCAll verifies that every v1 activation is reported by All,
// including one interrupted before it completed, exactly as v1's own
// native collector always reported it: unconditionally, without
// filtering by completeness.
func TestV1GCAll(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t)
	mm := m.(*mountManager)

	seedV1(t, mm.db, "test",
		v1Activation{name: "complete", active: []v1Active{{typ: "noop", mp: "/legacy/a", at: time.Now()}}},
		v1Activation{name: "incomplete", active: nil},
	)

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	defer cc.Cancel()

	var all []string
	cc.All(func(n gc.Node) { all = append(all, n.Key) })
	assert.ElementsMatch(t, []string{"complete", "incomplete"}, all)
}

// TestV1GCRemove verifies that removing a v1 activation through
// garbage collection releases and unmounts it.
func TestV1GCRemove(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	unmounts := new(atomic.Int32)
	m, targetdir := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: mountC, unmountAttempts: unmounts}))
	mm := m.(*mountManager)

	target := filepath.Join(targetdir, "1")
	require.NoError(t, os.MkdirAll(target, 0700))

	seedV1(t, mm.db, "test", v1Activation{
		name:   "old",
		active: []v1Active{{typ: "noop", mp: filepath.Join(target, "0"), at: time.Now()}},
	})

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	cc.Remove(gc.Node{Type: metadata.ResourceMount, Namespace: "test", Key: "old"})
	require.NoError(t, cc.Finish())

	assert.Equal(t, int32(1), unmounts.Load())
	_, err = os.Stat(target)
	assert.True(t, os.IsNotExist(err))
	_, err = m.Info(ctx, "old")
	assert.True(t, errdefs.IsNotFound(err))
}

// TestV1GCBackRefKeepsAlive verifies that a v1 activation's own
// gc.bref labels are honored by ActiveWithBackRefs, the same as a v2
// activation's: a mount predating this schema is not left unprotected
// from a still-live resource that backreferences it.
func TestV1GCBackRefKeepsAlive(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t)
	mm := m.(*mountManager)

	seedV1(t, mm.db, "test", v1Activation{
		name:   "old",
		labels: map[string]string{"containerd.io/gc.bref.container": "c1"},
		active: []v1Active{{typ: "noop", mp: "/legacy/old", at: time.Now()}},
	})

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	defer cc.Cancel()

	ccb := cc.(interface {
		ActiveWithBackRefs(string, func(gc.Node), func(gc.Node, gc.Node))
	})
	var brefs []string
	ccb.ActiveWithBackRefs("test", func(gc.Node) {}, func(n, ref gc.Node) {
		if n.Type == metadata.ResourceContainer && n.Key == "c1" {
			brefs = append(brefs, ref.Key)
		}
	})
	assert.Contains(t, brefs, "old")
}

// TestV1GCLeasedKeepsAlive verifies that a v1 activation's own lease
// membership is honored by Leased, the same as a v2 activation's.
func TestV1GCLeasedKeepsAlive(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t)
	mm := m.(*mountManager)

	seedV1(t, mm.db, "test", v1Activation{
		name:   "old",
		lease:  "L",
		active: []v1Active{{typ: "noop", mp: "/legacy/old", at: time.Now()}},
	})

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	defer cc.Cancel()

	ccl := cc.(interface {
		Leased(string, string, func(gc.Node))
	})
	var leased []string
	ccl.Leased("test", "L", func(n gc.Node) { leased = append(leased, n.Key) })
	assert.Contains(t, leased, "old")
}

// TestV1OrphanDirs verifies that a v1 activation's target directory,
// left behind with no database record at all, for example because
// the process died between mounting and completing the activation
// that created it, is reaped by garbage collection: the v1 equivalent
// of TestGCOrphanedBackingMount.
func TestV1OrphanDirs(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	handler := &noopHandler{mounts: mountC}
	m, targetdir := mkTestManager(t, WithMountHandler("noop", handler))

	orphan := filepath.Join(targetdir, "42")
	require.NoError(t, os.MkdirAll(orphan, 0700))
	mp0 := filepath.Join(orphan, "0")
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "0-type"), []byte("noop"), 0600))
	require.NoError(t, os.MkdirAll(mp0, 0700))
	mountC.Add(1)
	handler.live = map[string]struct{}{mp0: {}}

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	require.NoError(t, cc.Finish())

	assert.Equal(t, int32(0), mountC.Load(), "the orphaned v1 mount should be unmounted with its handler")
	_, err = os.Stat(orphan)
	assert.True(t, os.IsNotExist(err), "the orphaned v1 target directory should be removed")
}

// TestV1OrphanDirsSkipsBackingDir verifies that the v1 orphan
// directory scan never mistakes v2's own backingDir for a v1 target
// directory left behind with no record: "b" is not a valid uint64, so
// this holds regardless, but is worth pinning down explicitly given
// how much the two schemas' cleanup depends on not stepping on one
// another.
func TestV1OrphanDirsSkipsBackingDir(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("vol", &noopHandler{mounts: mountC}))

	// A real v2 mount, whose directory lives under backingDir.
	_, err := m.Activate(ctx, "a", []mount.Mount{{Type: "vol", Source: "/dev/null", Options: []string{"rw"}}})
	require.NoError(t, err)

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	require.NoError(t, cc.Finish())

	// The v2 mount must have survived: the v1 orphan scan must not
	// have swept backingDir out from under it.
	assert.Equal(t, int32(1), mountC.Load())

	require.NoError(t, m.Deactivate(ctx, "a"))
}

// TestV1AndV2IDsCanCollideNumerically verifies that a v1 activation
// and a v2 mounted record which happen to share the same numeric id,
// drawn from two entirely independent counters, do not interfere with
// one another: neither's directory, under different roots (a bare
// number for v1, backingDir for v2), is ever mistaken for the
// other's, and a garbage collection pass which touches only one
// leaves the other untouched.
func TestV1AndV2IDsCanCollideNumerically(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, targetdir := mkTestManager(t, WithMountHandler("vol", &noopHandler{mounts: mountC}))
	mm := m.(*mountManager)

	ainfo, err := m.Activate(ctx, "new", []mount.Mount{{Type: "vol", Source: "/dev/null", Options: []string{"rw"}}})
	require.NoError(t, err)
	v2MP := ainfo.Active[0].MountPoint

	// Extract the v2 record's own numeric id from its mount point
	// (<targets>/backingDir/<id>/fs) and seed a v1 activation with
	// that same id directly: v1's and v2's id sequences are entirely
	// independent counters, so which numbers actually collide is an
	// implementation detail neither this test nor this package should
	// depend on, only that a collision, however it happens, is safe.
	id := filepath.Base(filepath.Dir(v2MP))
	require.NoError(t, mm.db.Update(func(tx *bolt.Tx) error {
		oldbkt, err := tx.CreateBucketIfNotExists([]byte("v1"))
		if err != nil {
			return err
		}
		nsbkt, err := oldbkt.CreateBucketIfNotExists([]byte("test"))
		if err != nil {
			return err
		}
		mbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyMounts)
		if err != nil {
			return err
		}
		bkt, err := mbkt.CreateBucket([]byte("old"))
		if err != nil {
			return err
		}
		idNum, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return err
		}
		idb, err := encodeID(idNum)
		if err != nil {
			return err
		}
		if err := bkt.Put(bucketKeyID, idb); err != nil {
			return err
		}
		abkt, err := bkt.CreateBucket(bucketKeyActive)
		if err != nil {
			return err
		}
		cur, err := abkt.CreateBucket([]byte{0})
		if err != nil {
			return err
		}
		if err := cur.Put(bucketKeyType, []byte("vol")); err != nil {
			return err
		}
		if err := cur.Put(bucketKeyMountPoint, []byte("/legacy/old")); err != nil {
			return err
		}
		atb, err := time.Now().MarshalBinary()
		if err != nil {
			return err
		}
		return cur.Put(bucketKeyMountedAt, atb)
	}))
	require.Equal(t, filepath.Join(targetdir, backingDir, id, mountPointName), v2MP,
		"sanity check on the path this test parsed the id out of")

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	cc.Remove(gc.Node{Type: metadata.ResourceMount, Namespace: "test", Key: "old"})
	require.NoError(t, cc.Finish())

	// The v1 activation is gone, but the v2 one, despite sharing its
	// numeric id, must be completely unaffected.
	_, err = m.Info(ctx, "old")
	assert.True(t, errdefs.IsNotFound(err))
	info, err := m.Info(ctx, "new")
	require.NoError(t, err)
	assert.Equal(t, v2MP, info.Active[0].MountPoint)
	assert.Equal(t, int32(1), mountC.Load())

	require.NoError(t, m.Deactivate(ctx, "new"))
}

// TestV1NeverModified verifies that a full v2 activate, deactivate and
// garbage collection cycle leaves an unrelated v1 activation, and the
// "v1" bucket itself, completely unchanged.
func TestV1NeverModified(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: new(atomic.Int32)}))
	mm := m.(*mountManager)

	seedV1(t, mm.db, "test", v1Activation{
		name:   "untouched",
		lease:  "L",
		labels: map[string]string{"containerd.io/gc.bref.container": "c1"},
		active: []v1Active{{typ: "noop", mp: "/legacy/untouched", at: time.Now()}},
	})

	var before []byte
	require.NoError(t, mm.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket([]byte("v1"))
		require.NotNil(t, bkt)
		var err error
		before, err = dumpBucket(bkt)
		return err
	}))

	_, err := m.Activate(ctx, "unrelated", []mount.Mount{{Type: "noop", Source: "/dev/null"}})
	require.NoError(t, err)
	require.NoError(t, m.Deactivate(ctx, "unrelated"))

	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	require.NoError(t, cc.Finish())

	var after []byte
	require.NoError(t, mm.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket([]byte("v1"))
		require.NotNil(t, bkt)
		var err error
		after, err = dumpBucket(bkt)
		return err
	}))

	assert.Equal(t, before, after, "v1 must be byte for byte unchanged")
}

// dumpBucket serializes a bucket's full contents, recursively, into a
// deterministic byte string for comparison.
func dumpBucket(bkt *bolt.Bucket) ([]byte, error) {
	var buf []byte
	c := bkt.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		buf = append(buf, k...)
		buf = append(buf, 0)
		if v != nil {
			buf = append(buf, v...)
			buf = append(buf, 0, 0)
			continue
		}
		sub, err := dumpBucket(bkt.Bucket(k))
		if err != nil {
			return nil, err
		}
		buf = append(buf, sub...)
		buf = append(buf, 0, 0)
	}
	return buf, nil
}
