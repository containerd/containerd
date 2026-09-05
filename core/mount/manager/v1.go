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
	"sort"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/metadata/boltutil"
	"github.com/containerd/containerd/v2/core/mount"
)

// This file is the entirety of what this package still knows about
// "v1", the schema it replaced: how to read an activation, release
// one, and see one from garbage collection. Nothing here writes to
// "v1", or builds a "v2" record from data "v1" recorded; a "v1"
// activation is read from and unmounted at the mount point it already
// has, never given a "v2" identity or moved under backingDir.
//
// Every access goes through tx.Bucket or getBucket, never
// CreateBucketIfNotExists, so a database or namespace with no "v1"
// data never gains a "v1" bucket just from being read.
//
// Because "v1" is never mutated, a rollback to a binary which only
// understands it is safe at any point: it finds "v1" exactly as it
// left it, minus whatever activations this package released.
//
// "v1" mounts are never deduplicated against one another or against a
// "v2" mount: shareable requires a source, and "v1" never recorded
// one (only type and mount point), so shareable already reports false
// for every "v1" mount without this package special casing it.
//
// This can be removed in 2.7: 2.6 is the next LTS, so "v1" support
// needs to survive it, and 2.7 is the next release after that free to
// make a breaking change; introducing and removing "v1" support in
// the same release (2.4) would leave no window for anyone to actually
// upgrade through it.

// v1's own key names, matching exactly what a "v1" binary wrote to
// disk. None of these are shared with buckets.go's "v2" keys, even
// where the spelling is identical today: a future rename of a "v2"
// key must not be able to silently change how "v1" is read, which
// sharing the constant would risk regardless of what either happens
// to be spelled today.
var (
	v1KeyID         = []byte("id")
	v1KeyMounts     = []byte("mounts")
	v1KeyLeases     = []byte("leases")
	v1KeyLease      = []byte("lease")
	v1KeyActive     = []byte("active")
	v1KeySystem     = []byte("system")
	v1KeyType       = []byte("type")
	v1KeySource     = []byte("source")
	v1KeyTarget     = []byte("target")
	v1KeyOptions    = []byte("options")
	v1KeyMountedAt  = []byte("mat")
	v1KeyMountPoint = []byte("mp")
)

// v1Position is one position in a v1 activation's mount chain, in the
// shape that schema recorded it.
type v1Position struct {
	mtype string
	point string
	at    *time.Time
}

// v1ReadID reads the numeric id a v1 activation was created with.
func v1ReadID(bkt *bolt.Bucket) uint64 {
	id, _ := binary.Uvarint(bkt.Get(v1KeyID))
	return id
}

// v1HasActive reports whether a v1 activation bucket has ever
// recorded a mount chain at all, complete or not. Its absence is v1's
// own completion marker: an activation interrupted before it finished
// never created it.
func v1HasActive(bkt *bolt.Bucket) bool {
	return bkt.Bucket(v1KeyActive) != nil
}

// v1Positions reads a v1 activation's mount chain, in order (base
// first, the same order the chain was originally mounted in; compare
// v1OrphanPositions, which must recover this same order from disk).
// It returns nil both when there is no active bucket at all and when
// there is one but it is empty; callers which need to tell those
// apart use v1HasActive.
func v1Positions(bkt *bolt.Bucket) []v1Position {
	abkt := bkt.Bucket(v1KeyActive)
	if abkt == nil {
		return nil
	}
	var positions []v1Position
	abkt.ForEachBucket(func(k []byte) error {
		cur := abkt.Bucket(k)
		p := v1Position{
			mtype: string(cur.Get(v1KeyType)),
			point: string(cur.Get(v1KeyMountPoint)),
		}
		if v := cur.Get(v1KeyMountedAt); len(v) > 0 {
			var at time.Time
			if err := at.UnmarshalBinary(v); err == nil {
				p.at = &at
			}
		}
		positions = append(positions, p)
		return nil
	})
	return positions
}

// v1SystemMounts reads a v1 activation's system mounts: the mounts
// left for the caller to perform, which v1 recorded in full (type,
// source, target and options), unlike the mount chain above, which it
// only ever recorded type and mount point for. Read in order, the
// same shape readSystemMount reads a "v2" activation's system mounts
// in, since v1 wrote them identically.
func v1SystemMounts(bkt *bolt.Bucket) ([]mount.Mount, error) {
	sbkt := bkt.Bucket(v1KeySystem)
	if sbkt == nil {
		return nil, nil
	}
	var system []mount.Mount
	if err := sbkt.ForEachBucket(func(k []byte) error {
		cur := sbkt.Bucket(k)
		m := mount.Mount{
			Type:   string(cur.Get(v1KeyType)),
			Source: string(cur.Get(v1KeySource)),
			Target: string(cur.Get(v1KeyTarget)),
		}
		if v := cur.Get(v1KeyOptions); len(v) > 0 {
			m.Options = strings.Split(string(v), "\x00")
		}
		system = append(system, m)
		return nil
	}); err != nil {
		return nil, err
	}
	return system, nil
}

// v1ActivationInfo builds ActivationInfo for a v1 activation: Active
// from its mount chain, System from the mounts it left for the caller
// to perform. An interrupted activation, with no active bucket, is
// reported with an empty Active list rather than as an error.
func v1ActivationInfo(name string, bkt *bolt.Bucket) (mount.ActivationInfo, error) {
	info := mount.ActivationInfo{Name: name}
	for _, p := range v1Positions(bkt) {
		info.Active = append(info.Active, mount.ActiveMount{
			Mount:      mount.Mount{Type: p.mtype},
			MountPoint: p.point,
			MountedAt:  p.at,
		})
	}
	system, err := v1SystemMounts(bkt)
	if err != nil {
		return mount.ActivationInfo{}, err
	}
	info.System = system
	lbls, err := boltutil.ReadLabels(bkt)
	if err != nil {
		return mount.ActivationInfo{}, err
	}
	info.Labels = lbls
	return info, nil
}

// v1Release deletes a v1 activation and its lease membership, if any,
// and returns the mount chain it described for the caller to unmount.
// It reports found=false if there is no v1 activation by this name.
func v1Release(tx *bolt.Tx, namespace, name string) (positions []v1Position, mid uint64, found bool, err error) {
	nsbkt := getBucket(tx, bucketKeyV1, []byte(namespace))
	if nsbkt == nil {
		return nil, 0, false, nil
	}
	mbkt := nsbkt.Bucket(v1KeyMounts)
	if mbkt == nil {
		return nil, 0, false, nil
	}
	bkt := mbkt.Bucket([]byte(name))
	if bkt == nil {
		return nil, 0, false, nil
	}

	mid = v1ReadID(bkt)
	positions = v1Positions(bkt)

	if lid := bkt.Get(v1KeyLease); len(lid) > 0 {
		if lsbkt := nsbkt.Bucket(v1KeyLeases); lsbkt != nil {
			if lbkt := lsbkt.Bucket(lid); lbkt != nil {
				if err := lbkt.Delete([]byte(name)); err != nil {
					return nil, 0, false, err
				}
				if k, _ := lbkt.Cursor().First(); k == nil {
					if err := lsbkt.DeleteBucket(lid); err != nil {
						return nil, 0, false, err
					}
				}
			}
		}
	}

	if err := mbkt.DeleteBucket([]byte(name)); err != nil {
		return nil, 0, false, err
	}

	// mbkt, nsbkt and bucketKeyV1 are left in place even once empty.
	// bucketKeyV1 owns the id sequence a v1 binary allocates mids
	// from; deleting it would restart that sequence at 1 after a
	// rollback, where a fresh mid could collide with an orphaned
	// "<targets>/1" directory not yet reaped. v1's own Mkdir has no
	// existing-directory tolerance, so such a collision fails the
	// activation.
	return positions, mid, true, nil
}

// v1Unmount unmounts a v1 activation's mount chain in reverse order
// and removes its target directory. Every attempt tolerates finding
// nothing there, the same as unmountRecords does for v2: a v1 mount
// carries no more guarantee of still being where it was left than a
// v2 one does.
func (mm *mountManager) v1Unmount(ctx context.Context, positions []v1Position, mid uint64) error {
	var errs []error
	for _, p := range slices.Backward(positions) {
		var err error
		if h := mm.handlers[p.mtype]; h != nil {
			err = h.Unmount(ctx, p.point)
		} else {
			err = mount.Unmount(p.point, 0)
		}
		if err != nil && !alreadyUnmounted(err) {
			errs = append(errs, fmt.Errorf("failed to unmount %q: %w", p.point, err))
		}
	}
	if err := os.RemoveAll(filepath.Join(mm.targets.Name(), strconv.FormatUint(mid, 10))); err != nil && !os.IsNotExist(err) {
		log.G(ctx).WithError(err).WithField("mountid", mid).Warn("failed to remove v1 mount target directory")
	}
	return errors.Join(errs...)
}

// v1All reports every v1 activation in tx to fn, including one
// interrupted before it completed.
func v1All(tx *bolt.Tx, fn func(namespace, name string)) {
	v1bkt := tx.Bucket(bucketKeyV1)
	if v1bkt == nil {
		return
	}
	nsc := v1bkt.Cursor()
	for nsk, nsv := nsc.First(); nsk != nil; nsk, nsv = nsc.Next() {
		if nsv != nil {
			continue
		}
		mbkt := v1bkt.Bucket(nsk).Bucket(v1KeyMounts)
		if mbkt == nil {
			continue
		}
		mc := mbkt.Cursor()
		for mk, mv := mc.First(); mk != nil; mk, mv = mc.Next() {
			if mv != nil {
				continue
			}
			fn(string(nsk), string(mk))
		}
	}
}

// v1Released describes one v1 activation removed, whether by
// v1ApplyRemoveNamespace or by the v1 orphan directory scan, carrying
// what v1Unmount needs to finish the job once the caller's
// transaction, if any, commits.
type v1Released struct {
	positions []v1Position
	mid       uint64
}

// v1ApplyRemoveNamespace deletes the v1 activations in namespace ns
// marked for removal in removed, mirroring applyRemove's own v2 walk
// in shape: every activation still in nsbkt afterward, not just the
// ones this releases, has its mid collected, since the orphan
// directory scan which runs once the caller's transaction commits
// needs to know about every v1 activation which survives, not only
// the ones touched here, to avoid mistaking a live one's directory
// for one nothing references any more.
//
// A name in removed which does not name a v1 activation in this
// namespace, whether because it never did or because it names a v2
// one instead, is silently ignored: applyRemove calls this once per
// namespace with the same removed set it uses for v2, rather than
// sorting it into schemas first. A v1 and a v2 activation which
// happen to share a name are unrelated resources, reachable at all
// only via a rollback to a v1 binary and forward again.
func v1ApplyRemoveNamespace(ctx context.Context, mm *mountManager, tx *bolt.Tx, ns string, nsbkt *bolt.Bucket, removed map[string]struct{}, mounted map[string]struct{}, haveMountTable bool) (released []v1Released, remainingMids map[uint64]struct{}, err error) {
	remainingMids = map[uint64]struct{}{}
	mbkt := nsbkt.Bucket(v1KeyMounts)
	if mbkt == nil {
		return nil, remainingMids, nil
	}

	// Collect first: releasing an activation writes to sibling
	// buckets (its lease membership), which must not happen while a
	// cursor is open over the mounts bucket. keep is gathered in the
	// same pass rather than by walking mbkt again afterward: it is
	// exactly the complement of remove, decided before either list is
	// acted on.
	var remove, keep [][]byte
	mc := mbkt.Cursor()
	for mk, mv := mc.First(); mk != nil; mk, mv = mc.Next() {
		if mv != nil {
			continue
		}
		if _, ok := removed[string(mk)]; ok {
			remove = append(remove, bytes.Clone(mk))
			continue
		}
		// Not marked for removal by the caller; reconcile it against
		// the mount table snapshot regardless, the same as applyRemove
		// does for v2 (see reconcile.go).
		live, lerr := v1ActivationLive(ctx, mm, mbkt.Bucket(mk), mounted, haveMountTable)
		if lerr != nil {
			return nil, nil, lerr
		}
		if !live {
			remove = append(remove, bytes.Clone(mk))
			continue
		}
		keep = append(keep, bytes.Clone(mk))
	}

	for _, mk := range remove {
		positions, mid, ok, err := v1Release(tx, ns, string(mk))
		if err != nil {
			return nil, nil, err
		}
		if ok {
			released = append(released, v1Released{positions: positions, mid: mid})
		}
	}

	for _, mk := range keep {
		remainingMids[v1ReadID(mbkt.Bucket(mk))] = struct{}{}
	}

	return released, remainingMids, nil
}

// v1OrphanDirs returns v1 mount chains found on disk with no
// surviving database record in any namespace, for example because
// the process died between mounting and completing the v1 activation
// that created them, the v1 equivalent of what orphanBackingMounts
// does for v2.
//
// remainingMids must be the union of every namespace's surviving v1
// mids, not just one namespace's: v1's directory names are a single
// flat id space, shared across every namespace, unlike v2's records,
// which live under backingDir and never share this space with
// anything.
func v1OrphanDirs(mm *mountManager, remainingMids map[uint64]struct{}) ([]v1Released, error) {
	fd, err := mm.targets.Open(".")
	if err != nil {
		return nil, err
	}
	defer fd.Close()

	dirs, err := fd.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	var orphaned []v1Released
	for _, d := range dirs {
		if d == backingDir {
			continue
		}
		mid, err := strconv.ParseUint(d, 10, 64)
		if err != nil {
			continue
		}
		if _, ok := remainingMids[mid]; ok {
			continue
		}

		positions, err := v1OrphanPositions(mm, d)
		if err != nil {
			return nil, err
		}
		orphaned = append(orphaned, v1Released{positions: positions, mid: mid})
	}

	return orphaned, nil
}

// v1OrphanPositions reconstructs a v1 activation's mount chain from
// the type file left alongside each of its mount points, the same
// fallback orphanBackingMounts uses for a v2 record found with no
// database entry: v1 wrote one next to every position, named
// "<n>-type" for zero-padded position n, next to the mount point
// itself, named "<n>".
//
// n is not mount order: v1 numbered a chain of N positions ci =
// N-i for position i, so the base of the chain, mounted first, has
// the largest n, and the last mount added, mounted last, has n = 1.
// Sorting numerically descending on n therefore recovers mount order,
// base first, matching what v1Positions reads back from the database
// for a chain that completed normally. Returning anything else here
// would silently disagree with v1Positions about what "positions"
// means, and v1Unmount, which reverses whatever order it is given to
// unmount top first, would unmount base first for one of the two
// sources and corrupt a still-mounted chain under it.
func v1OrphanPositions(mm *mountManager, dir string) ([]v1Position, error) {
	full := filepath.Join(mm.targets.Name(), dir)
	fd, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer fd.Close()

	entries, err := fd.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	type numbered struct {
		n    int
		name string
	}
	var found []numbered
	for _, e := range entries {
		name, ok := strings.CutSuffix(e, "-type")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(name)
		if err != nil {
			// Not one of v1's own "<n>-type" files; tolerate it
			// defensively rather than fail the whole scan over it.
			continue
		}
		found = append(found, numbered{n: n, name: name})
	}
	sort.Slice(found, func(a, b int) bool { return found[a].n > found[b].n })

	var positions []v1Position
	for _, e := range found {
		mtype, err := os.ReadFile(filepath.Join(full, e.name+"-type"))
		if err != nil {
			return nil, err
		}
		positions = append(positions, v1Position{
			mtype: string(mtype),
			point: filepath.Join(full, e.name),
		})
	}

	return positions, nil
}
