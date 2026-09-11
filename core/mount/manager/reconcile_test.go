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
	"runtime"
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
// mounted is released by a collection pass.
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

// TestReconcileRemovesUnrealizedActivationNoHandler verifies the same
// crash window as TestReconcileRemovesUnrealizedActivation, but for a
// position with no Handler, unmounted directly with mount.Unmount.
func TestReconcileRemovesUnrealizedActivationNoHandler(t *testing.T) {
	if !canObserveMountTableOS {
		t.Skip("this handler-less position's fallback is the host mount table, which is not observable on this platform (see canObserveMountTableOS); it is correctly assumed live instead, covered by TestReconcileLiveAssumedWhenMountTableUnobservable")
	}
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t)
	mm := m.(*mountManager)

	stale := mount.Mount{Type: "bind", Source: testDevZero}
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
}

// TestReconcileRemovesActivationMissingBoundaryEnsure verifies that
// reconciliation releases an activation whose boundary mount's own
// deferred mkdir/mkfs never produced its recorded ensure target.
func TestReconcileRemovesActivationMissingBoundaryEnsure(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	m, _ := mkTestManager(t)
	mm := m.(*mountManager)

	missing := filepath.Join(t.TempDir(), "never-created")
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
		bkt, err := mbkt.CreateBucket([]byte("a"))
		if err != nil {
			return err
		}
		return bkt.Put(bucketKeyEnsureTargets, []byte(missing))
	}))

	require.NoError(t, startCollection(t, m, ctx).Finish())

	_, err := m.Info(ctx, "a")
	assert.True(t, errdefs.IsNotFound(err), "an activation whose boundary mount's ensure target does not exist must be reconciled away, got %v", err)
}

// TestReconcileRemovesPartiallyRealizedActivation verifies that an
// activation with a mixed chain -- one position mounted, the next
// never reached -- is released in full, unmounting the live position.
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

// opaqueHandler performs a mount that never appears in the host mount
// table, and implements no mount.MountedChecker.
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

// opaqueHandlerAlwaysLive implements mount.MountedChecker, always
// reporting live.
type opaqueHandlerAlwaysLive struct{ opaqueHandler }

func (h *opaqueHandlerAlwaysLive) Mounted(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// TestReconcileTrustsMountedChecker verifies that a Handler's own
// mount.MountedChecker answer is authoritative and is not
// second-guessed against the host mount table.
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

// TestReconcileOmittingCheckerFallsBackToHostMountTable verifies that
// a Handler which does not implement mount.MountedChecker is checked
// against the host mount table and reconciled away if absent from it.
func TestReconcileOmittingCheckerFallsBackToHostMountTable(t *testing.T) {
	if !canObserveMountTableOS {
		t.Skip("this Handler's fallback is the host mount table, which is not observable on this platform (see canObserveMountTableOS); it is correctly assumed live instead, covered by TestReconcileLiveAssumedWhenMountTableUnobservable")
	}
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
// closed, signaling entered once it does.
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

func (h *blockingUnmountHandler) Mounted(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// TestReconcileUnmountSerializesWithConcurrentActivate verifies that
// unmounting a record reconciliation released does not run
// concurrently with a fresh Activate resolving to the same identity.
func TestReconcileUnmountSerializesWithConcurrentActivate(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	handler := &blockingUnmountHandler{entered: make(chan struct{}), release: make(chan struct{})}
	m, _ := mkTestManager(t, WithMountHandler("vol", handler))
	mm := m.(*mountManager)

	vol := mount.Mount{Type: "vol", Source: testDevNull, Options: []string{"rw"}}

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

	// "b" resolves to the same identity while "a"'s unmount is in flight.
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

// TestReconcileWaitsForInFlightActivate verifies that StartCollection
// cannot observe an activation mid-realization.
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

// TestReconcileLiveAssumedWhenMountTableUnobservable verifies that a
// handler-less mount is assumed live when haveMountTable is false.
func TestReconcileLiveAssumedWhenMountTableUnobservable(t *testing.T) {
	ctx := context.Background()
	live, err := reconcileLive(ctx, nil, "/some/path/nothing/wrote", nil, false)
	require.NoError(t, err)
	assert.True(t, live, "a handler-less mount must be assumed live, never deleted, when the mount table cannot be trusted at all")
}

// TestReconcileLiveResolvesUnresolvedV1Path verifies that an
// unresolved v1 path still matches its canonical mount table entry.
// /proc stands in for a real mount reached through a symlink.
func TestReconcileLiveResolvesUnresolvedV1Path(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("relies on /proc, specific to Linux")
	}

	real, err := filepath.EvalSymlinks("/proc")
	require.NoError(t, err)

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))

	mounted := map[string]struct{}{real: {}}
	live, err := reconcileLive(context.Background(), nil, link, mounted, true)
	require.NoError(t, err)
	assert.True(t, live, "a v1 position's unresolved path must still match the canonical mount table entry")
}

// TestSnapshotMountTableFindsMountUnderTargets verifies that
// snapshotMountTable finds a mount under mm.targets. /proc stands in
// for a mount genuinely under the target directory, without needing
// root; NewManager is what guarantees mm.targets.Name() is canonical
// (see TestNewManagerResolvesSymlinkedTargetDir).
func TestSnapshotMountTableFindsMountUnderTargets(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("relies on /proc, specific to Linux")
	}

	real, err := filepath.EvalSymlinks("/proc")
	require.NoError(t, err)

	r, err := os.OpenRoot(real)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	mm := &mountManager{targets: r}
	mounted, haveMountTable := mm.snapshotMountTable(context.Background())
	require.True(t, haveMountTable)
	_, ok := mounted[real]
	assert.True(t, ok, "a mount under the target directory must be found")
}
