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
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moby/sys/mountinfo"
	bolt "go.etcd.io/bbolt"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/metadata/boltutil"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/kmutex"
	"github.com/containerd/containerd/v2/pkg/gc"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

type BoltManager interface {
	mount.Manager
	metadata.Collector
	Sync(context.Context) error
}

type managerOptions struct {
	handlers map[string]mount.Handler
	roots    []*os.Root
}

type Opt func(*managerOptions) error

func WithMountHandler(name string, h mount.Handler) Opt {
	return func(o *managerOptions) error {
		if o.handlers == nil {
			o.handlers = make(map[string]mount.Handler)
		}
		o.handlers[name] = h
		return nil
	}
}

func WithAllowedRoot(root string) Opt {
	return func(o *managerOptions) error {
		r, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		o.roots = append(o.roots, r)
		return nil
	}
}

func NewManager(db *bolt.DB, targetDir string, opts ...Opt) (mount.Manager, error) {
	options := managerOptions{}
	for _, o := range opts {
		if err := o(&options); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return nil, err
	}
	tr, err := os.OpenRoot(targetDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open target root %q: %w", targetDir, err)
	}
	// Mount points are owned by mounted records rather than by the
	// activation which created them, since a record may back several
	// activations.
	if err := tr.Mkdir(backingDir, 0700); err != nil && !os.IsExist(err) {
		tr.Close()
		return nil, fmt.Errorf("failed to create backing dir under %q: %w", targetDir, err)
	}
	rootMap := map[string]*os.Root{
		tr.Name(): tr,
	}
	for _, r := range options.roots {
		rootMap[r.Name()] = r
	}

	return &mountManager{
		db:       db,
		targets:  tr,
		handlers: options.handlers,
		rootMap:  rootMap,
		activate: kmutex.New(),
		mounting: kmutex.New(),
	}, nil
}

// boltDB is the subset of *bolt.DB the manager uses. It exists so a
// test can wrap a real database to count transactions, for example to
// verify that Activate performs exactly the write transactions it
// means to and no more; NewManager's own signature is unaffected,
// since a *bolt.DB satisfies this implicitly.
type boltDB interface {
	Update(func(*bolt.Tx) error) error
	View(func(*bolt.Tx) error) error
	Begin(writable bool) (*bolt.Tx, error)
	Close() error
}

type mountManager struct {
	db       boltDB
	targets  *os.Root
	handlers map[string]mount.Handler
	rootMap  map[string]*os.Root

	rwlock   sync.RWMutex
	activate kmutex.KeyedLocker
	// mounting serializes resolution and mounting of activations which
	// resolve to the same mount, keyed by mount identity, so that only
	// one of them performs the underlying mount.
	mounting kmutex.KeyedLocker
}

func (mm *mountManager) Close() error {
	var errs []error
	for _, r := range mm.rootMap {
		if err := r.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, mm.db.Close())
	return errors.Join(errs...)
}

func (mm *mountManager) Activate(ctx context.Context, name string, mounts []mount.Mount, opts ...mount.ActivateOpt) (info mount.ActivationInfo, retErr error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return mount.ActivationInfo{}, err
	}

	// Serialize concurrent activations of the same name to prevent a
	// racing Activate from misidentifying an in-progress activation as
	// a stale record and destroying it.
	if err := mm.activate.Lock(ctx, name); err != nil {
		return mount.ActivationInfo{}, err
	}
	defer mm.activate.Unlock(name)

	log.G(ctx).WithField("name", name).WithField("mounts", mounts).Debugf("activating mount")

	lid, leased := leases.FromContext(ctx)

	var config mount.ActivateOptions
	for _, opt := range opts {
		opt(&config)
	}

	// Transformation rewrites mounts in place, don't mutate the
	// caller's slice.
	if len(mounts) > 0 {
		local := make([]mount.Mount, len(mounts))
		copy(local, mounts)
		mounts = local
	}

	shouldTransform := func(p string, t string) bool {
		p = p + "/*"
		for _, mt := range config.AllowMountTypes {
			if mt == p || mt == t {
				return false
			}
		}
		return true
	}

	shouldHandle := func(t string) bool {
		return !slices.Contains(config.AllowMountTypes, t)
	}

	transforms := map[string]mount.Transformer{
		"format": mountFormatter{},
		"mkfs": &mkfs{
			rootMap: mm.rootMap,
		},
		"mkdir": &mkdir{
			rootMap: mm.rootMap,
		},
	}

	start := time.Now()
	// highest index of a mount
	// first system mount is the first index which should be mounted by the system
	var firstSystemMount = -1
	var mountConv [][]mount.Transformer
	var handlers []mount.Handler
	for i := range mounts {
		mountType := mounts[i].Type

		// Check is the source needs transformation, any transform operation requires
		// mounting with the mount manager.
		for transformType, mt, ok := strings.Cut(mountType, "/"); ok; transformType, mt, ok = strings.Cut(mountType, "/") {
			if tr, ok := transforms[transformType]; ok {
				if shouldTransform(transformType, mounts[i].Type) {
					// At least everything before this must be mounted
					// by the mount manager
					firstSystemMount = i
				}

				if handlers == nil {
					handlers = make([]mount.Handler, len(mounts))
				}

				if mountConv == nil {
					mountConv = make([][]mount.Transformer, len(mounts))
				}

				mountConv[i] = append(mountConv[i], typeTransformer{
					Transformer: tr,
					mountType:   mt,
				})

				mountType = mt
			} else {
				log.G(ctx).Warnf("unknown transform %q for mount %v", transformType, mounts[i])
				break
			}
		}

		var handler mount.Handler
		if mm.handlers != nil {
			handler = mm.handlers[mountType]
		}

		if handler != nil || config.Temporary {
			if handlers == nil {
				handlers = make([]mount.Handler, len(mounts))
			}
			handlers[i] = handler
			if shouldHandle(mountType) || config.Temporary {
				firstSystemMount = i + 1
			}
		}
	}
	// If no mounts are handled here, return not implemented and caller
	// may just perform system mounts as normal.
	if firstSystemMount == -1 {
		return mount.ActivationInfo{}, errdefs.ErrNotImplemented
	}
	if firstSystemMount > 255 {
		return mount.ActivationInfo{}, fmt.Errorf("too many mounts (%d): maximum 255: %w", firstSystemMount, errdefs.ErrInvalidArgument)
	}

	// Get read lock to block GC context from starting
	mm.rwlock.RLock()
	defer mm.rwlock.RUnlock()

	// A name already in use, in either schema, might be a genuinely
	// complete activation, which must be reported as already
	// existing, or wreckage left by an interruption, which must be
	// replaced. Telling those apart requires probing the mounts the
	// existing activation uses, which must happen without a
	// transaction open; see staleCollision.
	found, isV1, live, err := mm.staleCollision(ctx, namespace, name)
	if err != nil {
		return mount.ActivationInfo{}, err
	}
	if found && live {
		return mount.ActivationInfo{}, fmt.Errorf("mount %q: %w", name, errdefs.ErrAlreadyExists)
	}

	var (
		mid uint64
		// Records released while replacing a stale v2 activation;
		// unmounted once the transaction below commits.
		staleRecords []mountedRecord
		// Set while replacing a stale v1 activation; unmounted once
		// the transaction below commits.
		staleV1 *v1Released

		// Populated while resolving the chain below, for the realize
		// step which follows the transaction.
		records         = make([]mountedRecord, firstSystemMount)
		posEnsures      = make([][]func(context.Context) error, firstSystemMount)
		boundaryEnsures []func(context.Context) error
		system          []mount.Mount
	)

	if err := mm.db.Update(func(tx *bolt.Tx) error {
		v2bkt, err := tx.CreateBucketIfNotExists(bucketKeyV2)
		if err != nil {
			return err
		}

		nsbkt, err := v2bkt.CreateBucketIfNotExists([]byte(namespace))
		if err != nil {
			return err
		}
		mbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyMounts)
		if err != nil {
			return err
		}

		if isV1 {
			// Established outside this transaction that this name's
			// v1 activation is not live. A concurrent Deactivate
			// could have already released it, since v1, unlike v2,
			// is never touched by anyone else's stale-collision
			// cleanup; found here means nothing further to do.
			positions, v1mid, releasedV1, err := v1Release(tx, namespace, name)
			if err != nil {
				return err
			}
			if releasedV1 {
				staleV1 = &v1Released{positions: positions, mid: v1mid}
			}
		}

		bkt, err := mbkt.CreateBucket([]byte(name))
		if err != nil {
			existing := mbkt.Bucket([]byte(name))
			if existing == nil {
				return err
			}
			// Established outside this transaction, under the same
			// per-name lock, that this activation is not currently
			// live. Nothing else can have changed that in the
			// meantime: only Deactivate can touch this bucket
			// concurrently, and only by deleting it wholesale, never
			// by reviving it.
			if lid := existing.Get(bucketKeyLease); len(lid) > 0 {
				if lsbkt := nsbkt.Bucket(bucketKeyLeases); lsbkt != nil {
					if lbkt := lsbkt.Bucket(lid); lbkt != nil {
						if err := lbkt.Delete([]byte(name)); err != nil {
							return err
						}
					}
				}
			}
			staleRecords, err = releaseMountedRecords(tx, namespace, name, activationUses(existing))
			if err != nil {
				return err
			}
			if err := mbkt.DeleteBucket([]byte(name)); err != nil {
				return err
			}
			bkt, err = mbkt.CreateBucket([]byte(name))
			if err != nil {
				return err
			}
		}

		mid, err = v2bkt.NextSequence()
		if err != nil {
			return err
		}

		idb, err := encodeID(mid)
		if err != nil {
			return err
		}
		if err = bkt.Put(bucketKeyID, idb); err != nil {
			return err
		}

		if err := boltutil.WriteLabels(bkt, config.Labels); err != nil {
			return err
		}

		if err := boltutil.WriteTimestamps(bkt, start, start); err != nil {
			return err
		}

		if leased {
			if err = bkt.Put(bucketKeyLease, []byte(lid)); err != nil {
				return err
			}

			lsbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyLeases)
			if err != nil {
				return err
			}
			lbkt, err := lsbkt.CreateBucketIfNotExists([]byte(lid))
			if err != nil {
				return err
			}
			if err := lbkt.Put([]byte(name), nil); err != nil {
				return err
			}
		}

		// Resolve the whole chain now, in this one transaction: every
		// position's final mount value, and for the managed prefix,
		// the mounted record it uses. Mount point and approximate
		// mount time are computed as part of this, before anything is
		// actually mounted; nothing on the success path writes to the
		// database again after this transaction commits.
		var resolvedActive []mount.ActiveMount
		for i, m := range mounts[:firstSystemMount] {
			var chain []mount.Transformer
			if mountConv != nil {
				chain = mountConv[i]
			}
			rewritten, ensures, err := rewritePosition(ctx, chain, m, resolvedActive)
			if err != nil {
				return err
			}
			mounts[i] = rewritten
			posEnsures[i] = ensures

			rec, err := resolvePosition(tx, mm.targets.Name(), namespace, name, i, rewritten, start)
			if err != nil {
				return err
			}
			records[i] = rec
			resolvedActive = append(resolvedActive, rec.active())
		}

		// If the first system mount also carries a transform, resolve
		// it too: there is no system mount to convert when every
		// mount was handled above.
		system = mounts[firstSystemMount:]
		if mountConv != nil && firstSystemMount < len(mounts) {
			rewritten, ensures, err := rewritePosition(ctx, mountConv[firstSystemMount], mounts[firstSystemMount], resolvedActive)
			if err != nil {
				return err
			}
			system = append([]mount.Mount{rewritten}, mounts[firstSystemMount+1:]...)
			boundaryEnsures = ensures
		}
		// If no system mounts, add a bind mount if temporary
		// TODO: Add config for whether to add the bind mount?
		if config.Temporary && firstSystemMount > 0 {
			system = append(system, mount.Mount{
				Type:    "bind",
				Source:  resolvedActive[firstSystemMount-1].MountPoint,
				Options: []string{"rbind"},
			})
		}

		if len(system) > 0 {
			if len(system) > 255 {
				return fmt.Errorf("too many system mounts (%d): maximum 255", len(system))
			}
			sbkt, err := bkt.CreateBucket(bucketKeySystem)
			if err != nil {
				return err
			}
			for i, sm := range system {
				cur, err := sbkt.CreateBucket([]byte{byte(i)})
				if err != nil {
					return err
				}
				if err = putSystemMount(cur, sm); err != nil {
					return err
				}
			}
		}

		return nil
	}); err != nil {
		return mount.ActivationInfo{}, err
	}

	if len(staleRecords) > 0 {
		if err := mm.unmountRecords(ctx, staleRecords); err != nil {
			log.G(ctx).WithError(err).WithField("name", name).Warn("failed to clean up stale activation mounts")
		}
	}
	if staleV1 != nil {
		if err := mm.v1Unmount(ctx, staleV1.positions, staleV1.mid); err != nil {
			log.G(ctx).WithError(err).WithField("name", name).Warn("failed to clean up stale v1 activation mounts")
		}
	}

	defer func() {
		// The transaction above already committed durably by this
		// point, so a failure from here on must release what it
		// resolved: a failure which instead rolls back that
		// transaction itself returns before this defer is even
		// registered, and leaves nothing behind to release.
		if retErr != nil {
			var orphaned []mountedRecord
			if err := mm.db.Update(func(tx *bolt.Tx) error {
				nsbkt := getBucket(tx, bucketKeyV2, []byte(namespace))
				if nsbkt == nil {
					return fmt.Errorf("missing namespace %q bucket: %w", namespace, errdefs.ErrUnknown)
				}

				mbkt := nsbkt.Bucket(bucketKeyMounts)
				if mbkt == nil {
					return fmt.Errorf("missing mounts bucket: %w", errdefs.ErrUnknown)
				}

				if leased {
					lsbkt := nsbkt.Bucket(bucketKeyLeases)
					if lsbkt != nil {
						lbkt := lsbkt.Bucket([]byte(lid))
						if lbkt != nil {
							lbkt.Delete([]byte(name))
							if k, _ := lbkt.Cursor().First(); k == nil {
								lsbkt.DeleteBucket([]byte(lid))
							}
						}
					}
				}

				bkt := mbkt.Bucket([]byte(name))
				if bkt == nil {
					return nil
				}

				var err error
				orphaned, err = releaseMountedRecords(tx, namespace, name, activationUses(bkt))
				if err != nil {
					return err
				}

				return mbkt.DeleteBucket([]byte(name))
			}); err != nil {
				log.G(ctx).WithError(err).WithField("name", name).Errorf("failed to rollback")
			}
			if err := mm.unmountRecords(ctx, orphaned); err != nil {
				log.G(ctx).WithError(err).WithField("name", name).Error("failed to cleanup mounts after failed activation")
			}
		}
	}()

	var active []mount.ActiveMount
	for i, rec := range records {
		am, err := mm.realizeMount(ctx, rec, handlers[i], posEnsures[i], active)
		if err != nil {
			return mount.ActivationInfo{}, err
		}
		active = append(active, am)
	}

	for _, ensure := range boundaryEnsures {
		if err := ensure(ctx); err != nil {
			return mount.ActivationInfo{}, err
		}
	}

	info.Name = name
	info.Active = active
	info.System = system
	info.Labels = config.Labels

	return
}

// staleCollision reports whether an activation named name already
// exists, in either schema, and if so whether it is still actually
// live. A fully live activation must be reported as already existing
// rather than replaced; one which is not is wreckage, left by an
// interruption in v2 or simply left over from before an upgrade in
// v1, and must be released and recreated. v2 is checked first; a v1
// activation is only relevant when no v2 one by this name exists.
//
// An activation with no managed positions at all, for example one
// whose whole chain is a single mount the caller handles itself, is
// vacuously live in both schemas, for the same reason in each: v2
// resolves a chain's entire managed prefix in one transaction, so if
// the bucket exists at all, that prefix, however short, was fully
// resolved; v1 only ever created its active bucket once it had
// finished doing the equivalent. A v1 activation with no active
// bucket at all was interrupted before it got that far and is never
// live, matching how this package treats an equivalent v2 one.
//
// This never holds a bolt transaction open while probing: what an
// existing activation uses is read in one short read only
// transaction, and probing happens after it closes, exactly like
// realizeMount does for a freshly resolved chain.
func (mm *mountManager) staleCollision(ctx context.Context, namespace, name string) (found, isV1, live bool, err error) {
	type ref struct {
		mtype string
		point string
	}
	var (
		refs        []ref
		vacuousLive bool
	)
	if err := mm.db.View(func(tx *bolt.Tx) error {
		if bkt := getBucket(tx, bucketKeyV2, []byte(namespace), bucketKeyMounts, []byte(name)); bkt != nil {
			found = true
			vacuousLive = true
			nsbkt := getBucket(tx, bucketKeyV2, []byte(namespace))
			for _, id := range activationUses(bkt) {
				b, ok, rerr := getMountedRecord(nsbkt, mountedKey(id))
				if rerr != nil {
					return rerr
				}
				if !ok {
					// A used record which no longer exists should
					// not be reachable: a record is never deleted
					// while anything still uses it. Tolerate it
					// defensively by treating the activation as
					// stale rather than failing outright.
					refs = append(refs, ref{})
					continue
				}
				refs = append(refs, ref{mtype: b.mount.Type, point: b.point})
			}
			return nil
		}

		bkt := getBucket(tx, bucketKeyV1, []byte(namespace), bucketKeyMounts, []byte(name))
		if bkt == nil {
			return nil
		}
		found = true
		isV1 = true
		vacuousLive = v1HasActive(bkt)
		for _, p := range v1Positions(bkt) {
			refs = append(refs, ref{mtype: p.mtype, point: p.point})
		}
		return nil
	}); err != nil {
		return false, false, false, err
	}
	if !found {
		return false, false, false, nil
	}

	live = vacuousLive
	for _, r := range refs {
		if r.point == "" {
			live = false
			continue
		}
		ok, perr := probeMounted(ctx, mm.handlers[r.mtype], r.point)
		if perr != nil {
			return true, isV1, false, perr
		}
		if !ok {
			live = false
		}
	}

	return true, isV1, live, nil
}

// probeMounted reports whether path, the mount point of a mounted
// record, currently has that mount in effect. A handler which
// implements mount.MountedChecker is asked directly; otherwise the
// host's mount table is consulted, which is only accurate for a
// system mount or a handler whose mount point really is a kernel
// mount; see mount.MountedChecker's doc for why some handlers must
// implement it instead of relying on this fallback.
//
// A path which does not exist at all is reported as not mounted
// rather than as an error: this is the ordinary state of a mounted
// record which has never been realized yet.
//
// On Windows, the fallback always reports false: mountinfo.Mounted
// has no implementation there. This is not currently reachable in
// practice, since nothing this package's own transforms produce on
// Windows resolves to a managed position at all, but would matter for
// a handler-less mount activated with WithTemporary, which does.
func probeMounted(ctx context.Context, handler mount.Handler, path string) (bool, error) {
	if mc, ok := handler.(mount.MountedChecker); ok {
		return mc.Mounted(ctx, path)
	}
	live, err := mountinfo.Mounted(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return live, nil
}

// realizeMount ensures rec's mount is actually in effect, mounting it
// if a check finds it is not, and returns it as an ActiveMount for the
// activation using it. active holds every earlier position in the
// same chain, already realized, for a handler which resolves relative
// to them.
//
// Concurrent activations which resolve to the same identity serialize
// here, on the mount's identity rather than on either activation, so
// that only one of them ever mounts it, and repairing one found not
// to be mounted, whether because it was never realized or because
// something outside this package tore it down, always happens in
// place: the record's id and mount point never change, so this never
// has to choose between two ids which both claim to describe what is
// really one mount.
//
// A handler is trusted to mount at exactly the path it is given:
// rec's point, not whatever MountPoint the handler's own return value
// reports, is what every later position was already resolved against
// and what this returns, so a handler which mounted somewhere else
// would already have escaped what any of this can still put right.
func (mm *mountManager) realizeMount(ctx context.Context, rec mountedRecord, handler mount.Handler, ensures []func(context.Context) error, active []mount.ActiveMount) (mount.ActiveMount, error) {
	if shareable(rec.mount) {
		key := mountingKey(rec.mount)
		if err := mm.mounting.Lock(ctx, key); err != nil {
			return mount.ActiveMount{}, err
		}
		defer mm.mounting.Unlock(key)
	}

	live, err := probeMounted(ctx, handler, rec.point)
	if err != nil {
		return mount.ActiveMount{}, fmt.Errorf("failed to check mount %q: %w", rec.point, err)
	}
	if live {
		log.G(ctx).WithFields(log.Fields{
			"mounted":    rec.id,
			"mountpoint": rec.point,
		}).Debug("reusing mounted record")
		return rec.active(), nil
	}

	if err := mm.prepareRecordDir(rec.point, rec.mount.Type, handler == nil); err != nil {
		return mount.ActiveMount{}, err
	}

	for _, ensure := range ensures {
		if err := ensure(ctx); err != nil {
			return mount.ActiveMount{}, err
		}
	}

	if handler != nil {
		if _, err := handler.Mount(ctx, rec.mount, rec.point, active); err != nil {
			return mount.ActiveMount{}, fmt.Errorf("mount handler failed %v: %w", rec.mount, err)
		}
	} else {
		if err := rec.mount.Mount(rec.point); err != nil {
			return mount.ActiveMount{}, fmt.Errorf("mount failed %v: %w", rec.mount, err)
		}
	}

	return rec.active(), nil
}

// prepareRecordDir ensures the directory scaffolding for a mounted
// record's mount point exists: its parent directory, a type file
// recording the mount type so the record can still be unmounted with
// the correct handler even if it is ever found without its database
// record (see orphanBackingMounts), and, when nothing else will
// create it, the mount point itself.
//
// point is always one this schema itself computed, under backingDir.
//
// The mount point itself is only created when the mount is performed
// directly. Handlers decide what belongs at the path they are given,
// which is not always a directory: the loopback handler, for example,
// puts a symlink to the loop device there.
func (mm *mountManager) prepareRecordDir(point, mountType string, createMountPoint bool) error {
	rel, err := filepath.Rel(mm.targets.Name(), point)
	if err != nil {
		return fmt.Errorf("mount point %q outside target root: %w", point, err)
	}
	dir := filepath.Dir(rel)
	if err := mm.targets.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to create mounted record dir: %w", err)
	}
	if err := mm.targets.WriteFile(filepath.Join(dir, typeFileName), []byte(mountType), 0600); err != nil {
		return err
	}
	if createMountPoint {
		if err := mm.targets.Mkdir(rel, 0700); err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to create mount point: %w", err)
		}
	}
	return nil
}

// alreadyUnmounted reports whether an unmount error means there was
// nothing mounted at the path, which is the desired end state. This
// happens for a record which was resolved but never actually mounted,
// for example because the activation using it was interrupted before
// realizing it.
func alreadyUnmounted(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTDIR)
}

// unmountRecords unmounts released mounted records and removes their
// directories. They are unmounted in the order returned by
// releaseMountedRecords, which places dependent mounts before the
// mounts they were built on.
//
// A record may never have actually been mounted, for example if the
// activation resolving it was interrupted before realizing it, so
// every unmount attempt tolerates finding nothing there, whether or
// not a handler is involved: this schema has no record of whether a
// mount was ever actually performed, only of whether it should be,
// and unmounting is how that is reconciled.
func (mm *mountManager) unmountRecords(ctx context.Context, records []mountedRecord) error {
	var errs []error
	for _, b := range records {
		var err error
		if h := mm.handlers[b.mount.Type]; h != nil {
			err = h.Unmount(ctx, b.point)
		} else {
			err = mount.Unmount(b.point, 0)
		}
		if err != nil && !alreadyUnmounted(err) {
			errs = append(errs, fmt.Errorf("failed to unmount %q: %w", b.point, err))
			continue
		}
		if err := os.RemoveAll(mm.backingRoot(b.id)); err != nil && !os.IsNotExist(err) {
			log.G(ctx).WithError(err).WithField("backing", b.id).Warn("failed to remove backing mount dir")
		}
	}
	return errors.Join(errs...)
}

func encodeID(id uint64) ([]byte, error) {
	var (
		buf       [binary.MaxVarintLen64]byte
		idEncoded = buf[:]
	)
	idEncoded = idEncoded[:binary.PutUvarint(idEncoded, id)]

	if len(idEncoded) == 0 {
		return nil, fmt.Errorf("failed encoding id = %v", id)
	}
	return idEncoded, nil
}

func putSystemMount(bkt *bolt.Bucket, m mount.Mount) error {
	if err := bkt.Put(bucketKeyType, []byte(m.Type)); err != nil {
		return err
	}
	if err := bkt.Put(bucketKeySource, []byte(m.Source)); err != nil {
		return err
	}
	if err := bkt.Put(bucketKeyTarget, []byte(m.Target)); err != nil {
		return err
	}
	if len(m.Options) > 0 {
		if err := bkt.Put(bucketKeyOptions, []byte(strings.Join(m.Options, "\x00"))); err != nil {
			return err
		}
	}
	return nil
}

func readSystemMount(bkt *bolt.Bucket) mount.Mount {
	m := mount.Mount{
		Type:   string(bkt.Get(bucketKeyType)),
		Source: string(bkt.Get(bucketKeySource)),
		Target: string(bkt.Get(bucketKeyTarget)),
	}
	if v := bkt.Get(bucketKeyOptions); len(v) > 0 {
		m.Options = strings.Split(string(v), "\x00")
	}
	return m
}

// readActivationInfo builds the activation info for a mount, resolving
// the mounted record for each position in its chain. nsbkt is the
// namespace bucket holding the mounted records.
func readActivationInfo(nsbkt *bolt.Bucket, name string, bkt *bolt.Bucket) (mount.ActivationInfo, error) {
	info := mount.ActivationInfo{
		Name: name,
	}
	if abkt := bkt.Bucket(bucketKeyActive); abkt != nil {
		if err := abkt.ForEachBucket(func(k []byte) error {
			key := abkt.Bucket(k).Get(bucketKeyUses)
			if len(key) == 0 {
				return nil
			}
			b, ok, err := getMountedRecord(nsbkt, key)
			if err != nil {
				return err
			}
			if !ok {
				// Defensive only: a record is never deleted while
				// anything still uses it, so a surviving activation
				// should never reference one that is gone. Report
				// what is rather than failing the whole listing.
				return nil
			}
			info.Active = append(info.Active, b.active())
			return nil
		}); err != nil {
			return mount.ActivationInfo{}, err
		}
	}
	if sbkt := bkt.Bucket(bucketKeySystem); sbkt != nil {
		if err := sbkt.ForEachBucket(func(k []byte) error {
			info.System = append(info.System, readSystemMount(sbkt.Bucket(k)))
			return nil
		}); err != nil {
			return mount.ActivationInfo{}, err
		}
	}
	lbls, err := boltutil.ReadLabels(bkt)
	if err != nil {
		return mount.ActivationInfo{}, err
	}
	info.Labels = lbls

	return info, nil
}

func getBucket(tx *bolt.Tx, keys ...[]byte) *bolt.Bucket {
	bkt := tx.Bucket(keys[0])
	if bkt == nil {
		return nil
	}

	return getSubBucket(bkt, keys[1:]...)
}

func getSubBucket(bkt *bolt.Bucket, keys ...[]byte) *bolt.Bucket {
	for _, key := range keys {
		bkt = bkt.Bucket(key)
		if bkt == nil {
			return nil
		}
	}

	return bkt
}

func (mm *mountManager) Deactivate(ctx context.Context, name string) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	// Get read lock to block GC context from starting
	mm.rwlock.RLock()
	defer mm.rwlock.RUnlock()

	var (
		released []mountedRecord
		v1pos    []v1Position
		v1mid    uint64
		isV1     bool
		found    bool
	)

	// First in a single transaction, drop the activation and release
	// its references. Only the mounts which nothing else references
	// come back for unmounting. v2 is checked first; v1 is only
	// relevant when no v2 activation by this name exists.
	if err := mm.db.Update(func(tx *bolt.Tx) error {
		bkt := getBucket(tx, bucketKeyV2, []byte(namespace), bucketKeyMounts, []byte(name))
		if bkt != nil {
			found = true
			nsbkt := getBucket(tx, bucketKeyV2, []byte(namespace))
			mbkt := nsbkt.Bucket(bucketKeyMounts)

			lid := bkt.Get(bucketKeyLease)
			if lid != nil {
				if lsbkt := getSubBucket(nsbkt, bucketKeyLeases, lid); lsbkt != nil {
					if err := lsbkt.Delete([]byte(name)); err != nil {
						return err
					}
				}
			}

			var err error
			released, err = releaseMountedRecords(tx, namespace, name, activationUses(bkt))
			if err != nil {
				return err
			}

			return mbkt.DeleteBucket([]byte(name))
		}

		positions, mid, ok, err := v1Release(tx, namespace, name)
		if err != nil {
			return err
		}
		if ok {
			found = true
			isV1 = true
			v1pos = positions
			v1mid = mid
		}
		return nil
	}); err != nil {
		return err
	}

	if !found {
		return fmt.Errorf("mount %q: %w", name, errdefs.ErrNotFound)
	}

	if isV1 {
		return mm.v1Unmount(ctx, v1pos, v1mid)
	}

	// TODO: Should this also be backgrounded, not much can be done on failure to unmount
	if err := mm.unmountRecords(ctx, released); err != nil {
		// Don't try to cleanup, GC will need to do the rest
		return err
	}

	return nil
}

func (mm *mountManager) Info(ctx context.Context, name string) (mount.ActivationInfo, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return mount.ActivationInfo{}, err
	}

	var info mount.ActivationInfo
	if err := mm.db.View(func(tx *bolt.Tx) error {
		var err error
		info, err = infoFromTx(tx, namespace, name)
		return err
	}); err != nil {
		return mount.ActivationInfo{}, err
	}
	return info, nil
}

// infoFromTx reads a single activation's info, from either schema,
// from an already open transaction, read only or writable. v2 is
// checked first; v1 is only relevant when no v2 activation by this
// name exists.
func infoFromTx(tx *bolt.Tx, namespace, name string) (mount.ActivationInfo, error) {
	if nsbkt := getBucket(tx, bucketKeyV2, []byte(namespace)); nsbkt != nil {
		if bkt := getSubBucket(nsbkt, bucketKeyMounts, []byte(name)); bkt != nil {
			return readActivationInfo(nsbkt, name, bkt)
		}
	}
	if bkt := getBucket(tx, bucketKeyV1, []byte(namespace), bucketKeyMounts, []byte(name)); bkt != nil {
		return v1ActivationInfo(name, bkt)
	}
	return mount.ActivationInfo{}, fmt.Errorf("mount %q %w", name, errdefs.ErrNotFound)
}

func (mm *mountManager) Update(context.Context, mount.ActivationInfo, ...string) (mount.ActivationInfo, error) {
	return mount.ActivationInfo{}, errdefs.ErrNotImplemented
}

func (mm *mountManager) List(ctx context.Context, filters ...string) ([]mount.ActivationInfo, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var infos []mount.ActivationInfo
	if err := mm.db.View(func(tx *bolt.Tx) error {
		var err error
		infos, err = listFromTx(tx, namespace)
		return err
	}); err != nil {
		return nil, err
	}
	return infos, nil
}

// listFromTx reads every activation in a namespace, from both
// schemas, from an already open transaction, read only or writable.
// A v1 activation whose name a v2 one also uses, reachable only via a
// rollback to a v1 binary and forward again, is omitted: the v2 one
// wins, matching infoFromTx.
func listFromTx(tx *bolt.Tx, namespace string) ([]mount.ActivationInfo, error) {
	var infos []mount.ActivationInfo
	seen := map[string]struct{}{}

	if nsbkt := getBucket(tx, bucketKeyV2, []byte(namespace)); nsbkt != nil {
		if mbkt := nsbkt.Bucket(bucketKeyMounts); mbkt != nil {
			if err := mbkt.ForEachBucket(func(k []byte) error {
				info, err := readActivationInfo(nsbkt, string(k), mbkt.Bucket(k))
				if err != nil {
					return err
				}
				infos = append(infos, info)
				seen[string(k)] = struct{}{}
				return nil
			}); err != nil {
				return nil, err
			}
		}
	}

	if v1mbkt := getBucket(tx, bucketKeyV1, []byte(namespace), bucketKeyMounts); v1mbkt != nil {
		if err := v1mbkt.ForEachBucket(func(k []byte) error {
			if _, ok := seen[string(k)]; ok {
				return nil
			}
			info, err := v1ActivationInfo(string(k), v1mbkt.Bucket(k))
			if err != nil {
				return err
			}
			infos = append(infos, info)
			return nil
		}); err != nil {
			return nil, err
		}
	}

	return infos, nil
}

func (mm *mountManager) StartCollection(ctx context.Context) (metadata.CollectionContext, error) {
	// lock now and collection will unlock on cancel or finish
	mm.rwlock.Lock()

	tx, err := mm.db.Begin(true)
	if err != nil {
		mm.rwlock.Unlock()
		return nil, err
	}

	return &collectionContext{
		ctx:         ctx,
		tx:          tx,
		manager:     mm,
		removed:     map[string]map[string]struct{}{},
		remainingV1: map[uint64]struct{}{},
	}, nil
}

func (mm *mountManager) ReferenceLabel() string {
	return "mount"
}

type collectionContext struct {
	ctx     context.Context
	tx      *bolt.Tx
	manager *mountManager
	removed map[string]map[string]struct{}

	// Mounted records released during applyRemove; they need
	// unmounting after the transaction commits.
	released []mountedRecord
	// v1 activations released during applyRemove; they need
	// unmounting after the transaction commits, the same as released
	// above but through v1Unmount instead of unmountRecords.
	releasedV1 []v1Released
	// The mid of every v1 activation, across every namespace, which
	// survives applyRemove, so the v1 orphan directory scan in Finish
	// does not mistake its directory for one nothing references.
	remainingV1 map[uint64]struct{}
}

func (cc *collectionContext) All(fn func(gc.Node)) {
	if v2bkt := cc.tx.Bucket(bucketKeyV2); v2bkt != nil {
		nsc := v2bkt.Cursor()
		for nsk, nsv := nsc.First(); nsk != nil; nsk, nsv = nsc.Next() {
			if nsv != nil {
				continue
			}
			mntsbkt := v2bkt.Bucket(nsk).Bucket(bucketKeyMounts)
			if mntsbkt == nil {
				continue
			}
			mc := mntsbkt.Cursor()
			for mk, mv := mc.First(); mk != nil; mk, mv = mc.Next() {
				if mv != nil {
					continue
				}
				fn(gc.Node{
					Type:      metadata.ResourceMount,
					Namespace: string(nsk),
					Key:       string(mk),
				})
			}
		}
	}

	// v1 activations are reported exactly as this package's own
	// native collector always reported them: unconditionally,
	// including one interrupted before it completed. A name also used
	// by a v2 activation, reachable only via a rollback to a v1
	// binary and forward again, is reported for both: they are
	// unrelated resources which happen to share a key, and whichever
	// one is unreachable is swept independently of the other.
	v1All(cc.tx, func(namespace, name string) {
		fn(gc.Node{
			Type:      metadata.ResourceMount,
			Namespace: namespace,
			Key:       name,
		})
	})
}

func gcnode(t gc.ResourceType, ns, key string) gc.Node {
	return gc.Node{
		Type:      t,
		Namespace: ns,
		Key:       key,
	}
}

// scanBackRefLabels reports every gc.bref.* label on a mount's labels
// bucket to bref, associating the resource it names with n, the
// mount. It is shared between v2 and v1: a mount predating this
// schema is backreferenced exactly the same way one created under it
// is, so a still-live container's gc.bref.container label, say,
// protects either equally from being swept.
func scanBackRefLabels(lbkt *bolt.Bucket, ns string, n gc.Node, bref func(gc.Node, gc.Node)) {
	if lbkt == nil {
		return
	}
	lc := lbkt.Cursor()
	for _, h := range []struct {
		key     []byte
		handler func([]byte, []byte)
	}{
		{
			key: labelGCContainerBackRef,
			handler: func(k, v []byte) {
				if ks := string(k); ks != string(labelGCContainerBackRef) {
					// Allow reference naming separated by . or /, ignore names
					if ks[len(labelGCContainerBackRef)] != '.' && ks[len(labelGCContainerBackRef)] != '/' {
						return
					}
				}

				bref(gcnode(metadata.ResourceContainer, ns, string(v)), n)
			},
		},
		{
			key: labelGCContentBackRef,
			handler: func(k, v []byte) {
				if ks := string(k); ks != string(labelGCContentBackRef) {
					// Allow reference naming separated by . or /, ignore names
					if ks[len(labelGCContentBackRef)] != '.' && ks[len(labelGCContentBackRef)] != '/' {
						return
					}
				}

				bref(gcnode(metadata.ResourceContent, ns, string(v)), n)
			},
		},
		{
			key: labelGCImageBackRef,
			handler: func(k, v []byte) {
				if ks := string(k); ks != string(labelGCImageBackRef) {
					// Allow reference naming separated by . or /, ignore names
					if ks[len(labelGCImageBackRef)] != '.' && ks[len(labelGCImageBackRef)] != '/' {
						return
					}
				}

				bref(gcnode(metadata.ResourceImage, ns, string(v)), n)
			},
		},
		{
			key: labelGCSnapBackRef,
			handler: func(k, v []byte) {
				snapshotter := k[len(labelGCSnapBackRef):]
				if i := bytes.IndexByte(snapshotter, '/'); i >= 0 {
					snapshotter = snapshotter[:i]
				}
				bref(gcnode(metadata.ResourceSnapshot, ns, fmt.Sprintf("%s/%s", snapshotter, v)), n)
			},
		},
		// TODO: Consider support for root/expire labels
	} {
		for k, v := lc.Seek(h.key); k != nil && bytes.HasPrefix(k, h.key); k, v = lc.Next() {
			h.handler(k, v)
		}
	}
}

func (cc *collectionContext) ActiveWithBackRefs(ns string, fn func(gc.Node), bref func(gc.Node, gc.Node)) {
	if nsbkt := getBucket(cc.tx, bucketKeyV2, []byte(ns), bucketKeyMounts); nsbkt != nil {
		mc := nsbkt.Cursor()
		for mk, mv := mc.First(); mk != nil; mk, mv = mc.Next() {
			if mv != nil {
				continue
			}
			n := gcnode(metadata.ResourceMount, ns, string(mk))
			scanBackRefLabels(nsbkt.Bucket(mk).Bucket(bucketKeyLabels), ns, n, bref)
		}
	}

	// v1 activations report the same backreferences from their own
	// labels, so a mount which predates this schema is not swept out
	// from under a still-live resource that backreferences it just
	// because of that.
	if nsbkt := getBucket(cc.tx, bucketKeyV1, []byte(ns), bucketKeyMounts); nsbkt != nil {
		mc := nsbkt.Cursor()
		for mk, mv := mc.First(); mk != nil; mk, mv = mc.Next() {
			if mv != nil {
				continue
			}
			n := gcnode(metadata.ResourceMount, ns, string(mk))
			scanBackRefLabels(nsbkt.Bucket(mk).Bucket(bucketKeyLabels), ns, n, bref)
		}
	}
}

func (cc *collectionContext) Active(ns string, fn func(gc.Node)) {
	cc.ActiveWithBackRefs(ns, fn, func(gc.Node, gc.Node) {})
}

func (cc *collectionContext) Leased(ns, lease string, fn func(gc.Node)) {
	if bkt := getBucket(cc.tx, bucketKeyV2, []byte(ns), bucketKeyLeases, []byte(lease)); bkt != nil {
		c := bkt.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			fn(gc.Node{
				Type:      metadata.ResourceMount,
				Namespace: ns,
				Key:       string(k),
			})
		}
	}
	// v1 activations report their own lease membership too, so one
	// still in a live lease is not swept just for predating this
	// schema.
	if bkt := getBucket(cc.tx, bucketKeyV1, []byte(ns), bucketKeyLeases, []byte(lease)); bkt != nil {
		c := bkt.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			fn(gc.Node{
				Type:      metadata.ResourceMount,
				Namespace: ns,
				Key:       string(k),
			})
		}
	}
}

func (cc *collectionContext) Remove(n gc.Node) {
	log.G(cc.ctx).WithFields(log.Fields{"namespace": n.Namespace, "name": n.Key}).Debugf("remove mount")
	if n.Type != metadata.ResourceMount {
		return
	}
	nmap, ok := cc.removed[n.Namespace]
	if ok {
		if _, ok = nmap[n.Key]; !ok {
			nmap[n.Key] = struct{}{}
		}
	} else {
		cc.removed[n.Namespace] = map[string]struct{}{
			n.Key: {},
		}
	}
}

func (cc *collectionContext) Cancel() (err error) {
	err = cc.tx.Rollback()
	cc.manager.rwlock.Unlock()
	return
}

func (cc *collectionContext) Finish() error {
	remaining, err := cc.applyRemove()
	if err != nil {
		if rerr := cc.tx.Rollback(); rerr != nil {
			err = errors.Join(err, rerr)
		}
	} else {
		err = cc.tx.Commit()
	}
	if err != nil {
		cc.manager.rwlock.Unlock()
		return err
	}

	// Mounted records released above are unmounted from their
	// database records, exclude them from the orphan scan so they are
	// not unmounted twice.
	for _, b := range cc.released {
		remaining[b.id] = struct{}{}
	}
	for _, v := range cc.releasedV1 {
		cc.remainingV1[v.mid] = struct{}{}
	}

	// TODO: Consider using unmount q
	orphaned, err := cc.orphanBackingMounts(remaining)
	if err != nil {
		cc.manager.rwlock.Unlock()
		return err
	}
	orphanedV1, err := v1OrphanDirs(cc.manager, cc.remainingV1)

	cc.manager.rwlock.Unlock()

	if err != nil {
		return err
	}

	var errs []error
	if err := cc.manager.unmountRecords(cc.ctx, cc.released); err != nil {
		errs = append(errs, err)
	}
	if err := cc.manager.unmountRecords(cc.ctx, orphaned); err != nil {
		errs = append(errs, err)
	}
	for _, v := range append(cc.releasedV1, orphanedV1...) {
		if err := cc.manager.v1Unmount(cc.ctx, v.positions, v.mid); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// applyRemove deletes the activations marked for removal and releases
// the references they held on their mounted records. It returns the
// set of mounted record ids which are still referenced.
func (cc *collectionContext) applyRemove() (map[uint64]struct{}, error) {
	remaining := map[uint64]struct{}{}

	// v1 is walked independently of, and identically in shape to, the
	// v2 walk below: every namespace with v1 data is visited whether
	// or not the caller marked anything in it for removal, because
	// the mid of every activation which survives, not just the ones
	// released here, must be known before the v1 orphan directory
	// scan in Finish can tell a live activation's directory apart
	// from one nothing references any more. See v1ApplyRemoveNamespace
	// for how a name shared by both schemas, reachable only via a
	// rollback and forward again, is handled: independently, since
	// they are unrelated resources which only happen to share a name.
	if v1bkt := cc.tx.Bucket(bucketKeyV1); v1bkt != nil {
		nsc := v1bkt.Cursor()
		for nsk, nsv := nsc.First(); nsk != nil; nsk, nsv = nsc.Next() {
			if nsv != nil {
				continue
			}
			namespace := string(nsk)
			releasedV1, remainingV1, err := v1ApplyRemoveNamespace(cc.tx, namespace, v1bkt.Bucket(nsk), cc.removed[namespace])
			if err != nil {
				return nil, err
			}
			cc.releasedV1 = append(cc.releasedV1, releasedV1...)
			for id := range remainingV1 {
				cc.remainingV1[id] = struct{}{}
			}
		}
	}

	v2bkt := cc.tx.Bucket(bucketKeyV2)
	if v2bkt == nil {
		return remaining, nil
	}
	nsc := v2bkt.Cursor()
	for nsk, nsv := nsc.First(); nsk != nil; nsk, nsv = nsc.Next() {
		if nsv != nil {
			continue
		}
		namespace := string(nsk)
		removed := cc.removed[namespace]
		nsbkt := v2bkt.Bucket(nsk)
		msbkt := nsbkt.Bucket(bucketKeyMounts)
		if msbkt != nil {
			lsbkt := nsbkt.Bucket(bucketKeyLeases)
			// Collect first: releasing mounted records writes to
			// sibling buckets, which must not happen while a cursor is
			// open over the mounts bucket.
			var remove [][]byte
			msc := msbkt.Cursor()
			for msk, msv := msc.First(); msk != nil; msk, msv = msc.Next() {
				if msv != nil {
					continue
				}
				if removed != nil {
					if _, ok := removed[string(msk)]; ok {
						remove = append(remove, bytes.Clone(msk))
					}
				}
			}

			for _, msk := range remove {
				mbkt := msbkt.Bucket(msk)
				if mbkt == nil {
					continue
				}
				if lsbkt != nil {
					lid := mbkt.Get(bucketKeyLease)
					if len(lid) > 0 {
						lbkt := lsbkt.Bucket(lid)
						if lbkt != nil {
							lbkt.Delete(msk)
							if k, _ := lbkt.Cursor().First(); k == nil {
								lsbkt.DeleteBucket(lid)
							}
						}
					}
				}
				released, err := releaseMountedRecords(cc.tx, namespace, string(msk), activationUses(mbkt))
				if err != nil {
					return nil, err
				}
				cc.released = append(cc.released, released...)
				if err := msbkt.DeleteBucket(msk); err != nil {
					return nil, err
				}
			}
		}

		// Everything still in the mounted bucket is either used by a
		// surviving activation or is one an in-flight activation
		// already resolved, whether or not it has been realized yet.
		if mtbkt := nsbkt.Bucket(bucketKeyMounted); mtbkt != nil {
			bc := mtbkt.Cursor()
			for bk, bv := bc.First(); bk != nil; bk, bv = bc.Next() {
				if bv != nil {
					continue
				}
				id, _ := binary.Uvarint(bk)
				remaining[id] = struct{}{}
			}
		}
	}

	sortUnmountOrder(cc.released)

	return remaining, nil
}

// orphanBackingMounts returns mounted records whose directory is still
// present under the target root but which no longer have a database
// record, for example because the process died between mounting and
// resolving the record. They are reconstructed from the type file
// written before mounting so the correct handler is used to unmount
// them.
func (cc *collectionContext) orphanBackingMounts(remaining map[uint64]struct{}) ([]mountedRecord, error) {
	root := filepath.Join(cc.manager.targets.Name(), backingDir)
	fd, err := os.Open(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer fd.Close()

	dirs, err := fd.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	var orphaned []mountedRecord
	for _, d := range dirs {
		id, err := strconv.ParseUint(d, 10, 64)
		if err != nil {
			continue
		}
		if _, ok := remaining[id]; ok {
			continue
		}
		b := mountedRecord{
			id:    id,
			point: filepath.Join(root, d, mountPointName),
		}
		if bs, err := os.ReadFile(filepath.Join(root, d, typeFileName)); err == nil {
			b.mount.Type = string(bs)
		} else if !os.IsNotExist(err) {
			return nil, err
		} else {
			log.G(cc.ctx).WithField("backing", id).Info("missing type file, attempting unmount with no handler")
		}
		orphaned = append(orphaned, b)
	}

	sortUnmountOrder(orphaned)

	return orphaned, nil
}
