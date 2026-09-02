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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/namespaces"

	bolt "go.etcd.io/bbolt"
)

// startCollection is a small helper: every test in this file needs
// the same type assertion mkTestManager's return value requires to
// reach StartCollection at all.
func startCollection(t *testing.T, m mount.Manager, ctx context.Context) metadata.CollectionContext {
	t.Helper()
	cc, err := m.(interface {
		StartCollection(context.Context) (metadata.CollectionContext, error)
	}).StartCollection(ctx)
	require.NoError(t, err)
	return cc
}

// TestReconcileRemovesUnrealizedActivation verifies that an activation
// whose write transaction committed but which was never actually
// mounted -- the process died between resolving and realizing it,
// matching TestActivateStaleIncomplete's own fixture for the same
// crash window -- is released by garbage collection even though
// nothing asked for it to be removed.
func TestReconcileRemovesUnrealizedActivation(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: mountC}))
	mm := m.(*mountManager)

	stale := mount.Mount{Type: "noop", Source: testDevZero}
	require.NoError(t, mm.db.Update(func(tx *bolt.Tx) error {
		v2bkt, err := tx.CreateBucketIfNotExists(bucketKeyV2)
		if err != nil {
			return err
		}
		nsbkt, err := v2bkt.CreateBucketIfNotExists([]byte("test"))
		if err != nil {
			return err
		}
		mbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyActivations)
		if err != nil {
			return err
		}
		if _, err := mbkt.CreateBucket([]byte("task1")); err != nil {
			return err
		}
		_, err = resolvePosition(tx, mm.targets.Name(), "test", "task1", 0, stale, time.Now())
		return err
	}))

	require.NoError(t, startCollection(t, m, ctx).Finish())

	_, err := m.Info(ctx, "task1")
	assert.True(t, errdefs.IsNotFound(err), "an activation never actually mounted must be reconciled away, got %v", err)
	assert.Equal(t, int32(0), mountC.Load())
}

// TestReconcileRemovesPartiallyRealizedActivation verifies that an
// activation with a mixed chain -- one position actually mounted
// before the process died, the next never reached -- is released in
// full, and that the position which really was mounted is unmounted
// too: activationLive requires every position live, matching
// staleCollision's own rule, so one dead position condemns the whole
// activation, not just itself.
func TestReconcileRemovesPartiallyRealizedActivation(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	handler := &noopHandler{mounts: mountC}
	m, _ := mkTestManager(t, WithMountHandler("noop", handler))
	mm := m.(*mountManager)

	base := mount.Mount{Type: "noop", Source: testDevNull}

	var basePoint string
	require.NoError(t, mm.db.Update(func(tx *bolt.Tx) error {
		v2bkt, err := tx.CreateBucketIfNotExists(bucketKeyV2)
		if err != nil {
			return err
		}
		nsbkt, err := v2bkt.CreateBucketIfNotExists([]byte("test"))
		if err != nil {
			return err
		}
		mbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyActivations)
		if err != nil {
			return err
		}
		if _, err := mbkt.CreateBucket([]byte("a")); err != nil {
			return err
		}
		rec0, err := resolvePosition(tx, mm.targets.Name(), "test", "a", 0, base, time.Now())
		if err != nil {
			return err
		}
		basePoint = rec0.point
		top := mount.Mount{Type: "noop", Source: rec0.point + "/upper"}
		_, err = resolvePosition(tx, mm.targets.Name(), "test", "a", 1, top, time.Now())
		return err
	}))

	// The process got as far as mounting the base before dying; the
	// second position was never realized at all.
	handler.live = map[string]struct{}{basePoint: {}}
	mountC.Store(1)

	require.NoError(t, startCollection(t, m, ctx).Finish())

	_, err := m.Info(ctx, "a")
	assert.True(t, errdefs.IsNotFound(err), "a partially realized activation must be reconciled away entirely, got %v", err)
	assert.Equal(t, int32(0), mountC.Load(), "the position which really was mounted must be unmounted too")
}

// TestReconcileLeavesLiveActivationUntouched is the critical negative
// case: a fully live activation must survive a collection pass
// completely unchanged, not merely avoid being unmounted.
func TestReconcileLeavesLiveActivationUntouched(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("noop", &noopHandler{mounts: mountC}))

	ainfo, err := m.Activate(ctx, "a", []mount.Mount{{Type: "noop", Source: testDevNull}})
	require.NoError(t, err)
	mp := ainfo.Active[0].MountPoint

	require.NoError(t, startCollection(t, m, ctx).Finish())

	info, err := m.Info(ctx, "a")
	require.NoError(t, err, "a fully live activation must survive reconciliation")
	require.Len(t, info.Active, 1)
	assert.Equal(t, mp, info.Active[0].MountPoint)
	assert.Equal(t, int32(1), mountC.Load())

	require.NoError(t, m.Deactivate(ctx, "a"))
	assert.Equal(t, int32(0), mountC.Load())
}

// opaqueHandler performs a mount but does nothing the host mount
// table would ever show for it: reconcileLive checks it against that
// table anyway, since it implements no mount.MountedChecker, so it is
// always found dead. opaqueHandlerAlwaysLive below pairs with it to
// cover the other half of reconcileLive's dispatch, a Handler which
// does implement mount.MountedChecker.
type opaqueHandler struct {
	mounts *atomic.Int32
}

func (h *opaqueHandler) Mount(_ context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	h.mounts.Add(1)
	now := time.Now()
	return mount.ActiveMount{Mount: m, MountedAt: &now, MountPoint: mp}, nil
}

func (h *opaqueHandler) Unmount(_ context.Context, _ string) error {
	h.mounts.Add(-1)
	return nil
}

// Mounted is only reached by reconcileLive when the type assertion to
// mount.MountedChecker succeeds, so opaqueHandler must be split into
// two concrete types, one with this method and one without, rather
// than a single type with a field switching its behavior: the
// assertion is on the type, not any value of it.
type opaqueHandlerAlwaysLive struct{ opaqueHandler }

func (h *opaqueHandlerAlwaysLive) Mounted(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// TestReconcileTrustsMountedChecker verifies that a Handler's own
// mount.MountedChecker answer is authoritative and is not
// second-guessed against the host mount table: reporting true is
// enough to survive reconciliation regardless of what, if anything,
// the host mount table shows for the same path. This is what
// loopback depends on: its mount point is a symlink to a device and
// never appears in the host mount table at all.
//
// Seeded directly rather than taken through Activate: this Handler's
// Mounted always answers true, and that same answer decides whether
// realizeMount calls Mount in the first place, so an activation
// actually taken through Activate with it would never have Mount
// called at all, not even the first time. That is real behavior, not
// a test artifact, and is not what this test is checking; seeding a
// committed-but-never-mounted record directly, the same as
// TestReconcileRemovesUnrealizedActivation does for the opposite
// case, isolates reconciliation's own half of this Handler's
// behavior from realizeMount's.
func TestReconcileTrustsMountedChecker(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	handler := &opaqueHandlerAlwaysLive{opaqueHandler{mounts: new(atomic.Int32)}}
	m, _ := mkTestManager(t, WithMountHandler("opaque", handler))
	mm := m.(*mountManager)

	opq := mount.Mount{Type: "opaque", Source: testDevNull}
	require.NoError(t, mm.db.Update(func(tx *bolt.Tx) error {
		v2bkt, err := tx.CreateBucketIfNotExists(bucketKeyV2)
		if err != nil {
			return err
		}
		nsbkt, err := v2bkt.CreateBucketIfNotExists([]byte("test"))
		if err != nil {
			return err
		}
		mbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyActivations)
		if err != nil {
			return err
		}
		if _, err := mbkt.CreateBucket([]byte("a")); err != nil {
			return err
		}
		_, err = resolvePosition(tx, mm.targets.Name(), "test", "a", 0, opq, time.Now())
		return err
	}))

	require.NoError(t, startCollection(t, m, ctx).Finish())

	info, err := m.Info(ctx, "a")
	require.NoError(t, err, "a handler reporting itself always live must never be reconciled away, even though nothing was ever really mounted")
	require.Len(t, info.Active, 1)
}

// TestReconcileOmittingCheckerFallsBackToHostMountTable verifies the
// other half of the same contract: a Handler which does not implement
// mount.MountedChecker at all is checked against the real host mount
// table, exactly like a mount with no Handler, and is reconciled away
// if its mount point does not actually appear there. This is the
// consequence of the doc's "not implementing MountedChecker is not
// unknown, it is a claim your mount point is a real kernel mount": a
// Handler which does not keep that claim true is not specially
// protected just for having a Handler at all.
func TestReconcileOmittingCheckerFallsBackToHostMountTable(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	mountC := new(atomic.Int32)
	m, _ := mkTestManager(t, WithMountHandler("opaque", &opaqueHandler{mounts: mountC}))

	ainfo, err := m.Activate(ctx, "a", []mount.Mount{{Type: "opaque", Source: testDevNull}})
	require.NoError(t, err)
	require.Len(t, ainfo.Active, 1)

	require.NoError(t, startCollection(t, m, ctx).Finish())

	_, err = m.Info(ctx, "a")
	assert.True(t, errdefs.IsNotFound(err),
		"a handler with no MountedChecker and no real kernel mount to show for it must be reconciled away, got %v", err)
	assert.Equal(t, int32(0), mountC.Load())
}

// blockingUnmountHandler blocks inside Unmount until release is
// closed, signaling entered once it does, so a test can force a
// deterministic window during which a concurrent Activate resolving
// to the same identity must wait for realizeMount's identity lock
// rather than mount while the old record sharing that identity is
// still being torn down.
type blockingUnmountHandler struct {
	entered     chan struct{}
	enteredOnce sync.Once
	release     chan struct{}
}

func (h *blockingUnmountHandler) Mount(_ context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	now := time.Now()
	return mount.ActiveMount{Mount: m, MountedAt: &now, MountPoint: mp}, nil
}

func (h *blockingUnmountHandler) Unmount(_ context.Context, _ string) error {
	h.enteredOnce.Do(func() { close(h.entered) })
	<-h.release
	return nil
}

// TestReconcileUnmountSerializesWithConcurrentActivate verifies that
// unmounting a record reconciliation released does not run
// concurrently with a fresh Activate resolving to the same identity:
// the record was already removed from the dedup index in the same
// transaction that released it, so without unmountRecord taking
// mm.mounting the same way realizeMount does, a concurrent Activate
// would mint a new record and mount it while the old one, sharing the
// same underlying resource, is still being unmounted.
func TestReconcileUnmountSerializesWithConcurrentActivate(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	handler := &blockingUnmountHandler{entered: make(chan struct{}), release: make(chan struct{})}
	m, _ := mkTestManager(t, WithMountHandler("vol", handler))
	mm := m.(*mountManager)

	vol := mount.Mount{Type: "vol", Source: testDevNull, Options: []string{"rw"}}

	// Committed but never actually mounted; reconciliation releases
	// it and, in doing so, must unmount it (defensively, the same as
	// any released record).
	require.NoError(t, mm.db.Update(func(tx *bolt.Tx) error {
		v2bkt, err := tx.CreateBucketIfNotExists(bucketKeyV2)
		if err != nil {
			return err
		}
		nsbkt, err := v2bkt.CreateBucketIfNotExists([]byte("test"))
		if err != nil {
			return err
		}
		mbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyActivations)
		if err != nil {
			return err
		}
		if _, err := mbkt.CreateBucket([]byte("a")); err != nil {
			return err
		}
		_, err = resolvePosition(tx, mm.targets.Name(), "test", "a", 0, vol, time.Now())
		return err
	}))

	cc := startCollection(t, m, ctx)

	finishDone := make(chan error, 1)
	go func() { finishDone <- cc.Finish() }()

	select {
	case <-handler.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reconciliation's unmount to start")
	}

	// "b" resolves to the same identity while "a"'s unmount is still
	// in flight. The dedup index entry is already gone by this point
	// -- released in the same transaction Finish already committed --
	// so Activate itself does not block; only realizeMount's identity
	// lock, taken by both sides, should make "b" wait to actually
	// mount.
	bDone := make(chan activateResult, 1)
	go func() {
		info, err := m.Activate(ctx, "b", []mount.Mount{vol})
		bDone <- activateResult{info, err}
	}()

	select {
	case <-bDone:
		t.Fatal("Activate must wait for the identity lock while a released record sharing it is still being unmounted")
	case <-time.After(50 * time.Millisecond):
	}

	close(handler.release)

	require.NoError(t, <-finishDone)
	b := <-bDone
	require.NoError(t, b.err)
	require.Len(t, b.info.Active, 1)

	require.NoError(t, m.Deactivate(ctx, "b"))
}

// TestReconcileWaitsForInFlightActivate verifies the invariant the
// whole feature depends on: StartCollection cannot observe an
// activation mid-realization, because it takes the same rwlock
// Activate holds, for reading, across resolving and realizing its
// entire chain. If this invariant were ever narrowed -- the snapshot
// taken later, or the lock released sooner -- reconciliation could
// observe a real activation before its mounts exist and delete it out
// from under the very call that is creating it.
func TestReconcileWaitsForInFlightActivate(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	handler := &blockingHandler{entered: make(chan struct{}), release: make(chan struct{})}
	m, _ := mkTestManager(t, WithMountHandler("blk", handler))

	activateDone := make(chan activateResult, 1)
	go func() {
		info, err := m.Activate(ctx, "a", []mount.Mount{{Type: "blk", Source: testDevNull}})
		activateDone <- activateResult{info, err}
	}()

	select {
	case <-handler.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Activate to start mounting")
	}

	collectDone := make(chan error, 1)
	go func() {
		cc := startCollection(t, m, ctx)
		collectDone <- cc.Finish()
	}()

	select {
	case <-collectDone:
		t.Fatal("StartCollection must block while Activate holds the rwlock across resolving and realizing its chain")
	case <-time.After(50 * time.Millisecond):
	}

	close(handler.release)

	a := <-activateDone
	require.NoError(t, a.err)
	require.NoError(t, <-collectDone)

	// The activation must have survived: reconciliation must never
	// have observed it mid-realization.
	info, err := m.Info(ctx, "a")
	require.NoError(t, err)
	require.Len(t, info.Active, 1)
	assert.Equal(t, a.info.Active[0].MountPoint, info.Active[0].MountPoint)

	require.NoError(t, m.Deactivate(ctx, "a"))
}

// TestReconcileLiveAssumedWhenMountTableUnobservable pins
// reconcileLive's contract directly, without depending on
// runtime.GOOS: a handler-less mount is assumed live, never deleted,
// exactly when haveMountTable is false, which canObserveMountTableOS
// forces on Windows and a failed read forces everywhere else.
func TestReconcileLiveAssumedWhenMountTableUnobservable(t *testing.T) {
	ctx := context.Background()
	live, err := reconcileLive(ctx, nil, "/some/path/nothing/wrote", nil, false)
	require.NoError(t, err)
	assert.True(t, live, "a handler-less mount must be assumed live, never deleted, when the mount table cannot be trusted at all")
}
