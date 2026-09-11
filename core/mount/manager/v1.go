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
// one, and see one from garbage collection. Nothing here migrates
// "v1" data into a "v2" record; a "v1" activation is read from and
// unmounted at the mount point it already has, never given a "v2"
// identity or moved under backingDir.
//
// Every access goes through tx.Bucket or getBucket, never
// CreateBucketIfNotExists, so a database or namespace with no "v1"
// data never gains a "v1" bucket just from being read.
//
// Releasing a "v1" activation deletes its own entry, never anything
// else's. A rollback to a binary which only understands "v1" finds it
// exactly as left, minus whatever this package released.
//
// "v1" mounts are never deduplicated against one another or against a
// "v2" mount: shareable requires a source, and "v1" never recorded
// one (only type and mount point), so shareable already reports false
// for every "v1" mount without this package special casing it.

// v1's own key names, matching exactly what a "v1" binary wrote to
// disk. Not shared with buckets.go's "v2" keys.
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

// v1HasActive reports whether a v1 activation bucket recorded a mount
// chain, complete or not.
func v1HasActive(bkt *bolt.Bucket) bool {
	return bkt.Bucket(v1KeyActive) != nil
}

// v1Positions reads a v1 activation's mount chain, base first. Returns
// nil both when there is no active bucket and when it is empty; use
// v1HasActive to tell those apart.
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
// left for the caller to perform.
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
	return positions, mid, true, nil
}

// v1Unmount unmounts a v1 activation's mount chain in reverse order
// and removes its target directory. Tolerates finding nothing there.
// The target directory is only removed once every position unmounted
// cleanly.
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
	if len(errs) == 0 {
		if err := os.RemoveAll(filepath.Join(mm.targets.Name(), strconv.FormatUint(mid, 10))); err != nil && !os.IsNotExist(err) {
			log.G(ctx).WithError(err).WithField("mountid", mid).Warn("failed to remove v1 mount target directory")
		}
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
// marked in removed or found no longer mounted, and returns the mid
// of every v1 activation which survives.
func v1ApplyRemoveNamespace(ctx context.Context, mm *mountManager, tx *bolt.Tx, ns string, nsbkt *bolt.Bucket, removed map[string]struct{}, mounted map[string]struct{}, haveMountTable bool) (released []v1Released, remainingMids map[uint64]struct{}, err error) {
	remainingMids = map[uint64]struct{}{}
	mbkt := nsbkt.Bucket(v1KeyMounts)
	if mbkt == nil {
		return nil, remainingMids, nil
	}

	// Collected first, before any cursor-invalidating writes.
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
// surviving database record in any namespace. remainingMids must be
// the union of every namespace's surviving v1 mids.
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
// the type file left alongside each mount point, named "<n>-type" for
// position n. v1 numbered a chain of N positions ci = N-i, so sorting
// numerically descending on n recovers mount order, base first.
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
