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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/mount"
)

// backingDir is the directory under the target root which holds the
// mount point of every mounted record created by this schema. A
// mounted record outlives the activation which created it, so it
// cannot live under a per activation directory. The short, non-numeric
// name also keeps it out of any numeric, per activation directories a
// different schema version may have created.
const backingDir = "b"

// mountPointName is the directory used as the mount point within a
// mounted record's directory. The sibling typeFileName records the
// mount type so a record's directory can still be unmounted with the
// correct handler when nothing else says how, for example if it is
// found orphaned after a restart.
const (
	mountPointName = "fs"
	typeFileName   = "type"
)

// mountedRecord is a mount performed by the manager. A single record
// may back several activations: activations which describe the same
// mount within a namespace use one record instead of each performing
// their own, so it stays mounted for as long as any activation uses
// it.
//
// point and at are always populated once a record exists, computed
// when it is resolved rather than measured after mounting succeeds
// (see the package doc). Neither is a reliable signal of whether the
// record is currently mounted; see mounted in manager.go for the
// check that answers that question.
type mountedRecord struct {
	id    uint64
	mount mount.Mount
	point string
	at    *time.Time
}

// active returns the mounted record as an ActiveMount for reporting
// through ActivationInfo.
func (b mountedRecord) active() mount.ActiveMount {
	return mount.ActiveMount{
		Mount:      b.mount,
		MountPoint: b.point,
		MountedAt:  b.at,
	}
}

// mountIdentity returns a digest which uniquely identifies the kernel
// mount described by m. Two mounts with the same identity in the same
// namespace resolve to the same filesystem, so the manager performs
// the mount once and reference counts it.
//
// Fields are length prefixed so that no combination of values can
// produce the same digest as a different combination. Option order is
// significant because it is significant to the kernel.
func mountIdentity(m mount.Mount) []byte {
	h := sha256.New()
	var lenbuf [8]byte
	write := func(s string) {
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(s)))
		h.Write(lenbuf[:])
		h.Write([]byte(s))
	}
	write(m.Type)
	write(m.Source)
	write(m.Target)
	binary.BigEndian.PutUint64(lenbuf[:], uint64(len(m.Options)))
	h.Write(lenbuf[:])
	for _, o := range m.Options {
		write(o)
	}
	return h.Sum(nil)
}

// shareable reports whether m may be satisfied by an existing,
// identical mounted record.
//
// Only mounts whose source names a concrete object in the filesystem
// are shared. Mounting the same image, block file or directory twice
// yields two views of the same data, so a single mount can serve every
// chain which references it. Filesystems which synthesize their
// contents instead (tmpfs, proc, sysfs, mqueue, devpts, overlay, ...)
// use a symbolic source such as "tmpfs" or "none"; two such mounts
// with identical parameters are still distinct filesystems and must
// not be collapsed into one.
func shareable(m mount.Mount) bool {
	return filepath.IsAbs(m.Source)
}

// mountedKey encodes a mounted record id for use as a bolt bucket key.
func mountedKey(id uint64) []byte {
	b, _ := encodeID(id)
	return b
}

// backingRoot returns the directory holding a mounted record's mount
// point and type file. Every mounted record was created at exactly
// this path, so deriving it from id here is always correct: no
// mounted record is ever built from v1 data (see v1.go).
func (mm *mountManager) backingRoot(id uint64) string {
	return filepath.Join(mm.targets.Name(), backingDir, strconv.FormatUint(id, 10))
}

// putMountedRecord writes a mounted record's identifying mount
// parameters together with the mount point and approximate mount time
// computed for it when it was resolved. The parameters are the
// identity the dedup index is built from and, like point and at, are
// written once, here, and never rewritten.
func putMountedRecord(bkt *bolt.Bucket, m mount.Mount, point string, at time.Time) error {
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
	if err := bkt.Put(bucketKeyMountPoint, []byte(point)); err != nil {
		return err
	}
	encoded, err := at.MarshalBinary()
	if err != nil {
		return err
	}
	return bkt.Put(bucketKeyMountedAt, encoded)
}

// readMountedRecord reads a full mounted record.
func readMountedRecord(id uint64, bkt *bolt.Bucket) (mountedRecord, error) {
	b := mountedRecord{
		id: id,
		mount: mount.Mount{
			Type:   string(bkt.Get(bucketKeyType)),
			Source: string(bkt.Get(bucketKeySource)),
			Target: string(bkt.Get(bucketKeyTarget)),
		},
		point: string(bkt.Get(bucketKeyMountPoint)),
	}
	if v := bkt.Get(bucketKeyOptions); len(v) > 0 {
		b.mount.Options = strings.Split(string(v), "\x00")
	}
	if v := bkt.Get(bucketKeyMountedAt); len(v) > 0 {
		var at time.Time
		if err := at.UnmarshalBinary(v); err != nil {
			return mountedRecord{}, err
		}
		b.at = &at
	}
	return b, nil
}

// getMountedRecord loads the mounted record with the given key from a
// namespace bucket, returning false when it no longer exists.
func getMountedRecord(nsbkt *bolt.Bucket, key []byte) (mountedRecord, bool, error) {
	mtbkt := nsbkt.Bucket(bucketKeyMounted)
	if mtbkt == nil {
		return mountedRecord{}, false, nil
	}
	bkt := mtbkt.Bucket(key)
	if bkt == nil {
		return mountedRecord{}, false, nil
	}
	id, _ := binary.Uvarint(key)
	b, err := readMountedRecord(id, bkt)
	if err != nil {
		return mountedRecord{}, false, err
	}
	return b, true, nil
}

// resolvePosition resolves m, already fully rewritten and therefore
// final, to a mounted record within tx, creating one if no shareable
// record with this identity already exists, and adds a reference from
// the named activation at the given chain position.
//
// The returned record's mount point and approximate mount time are
// always populated, whether the record is newly created or an
// existing one is being reused: whether anything is actually mounted
// there is a question this never answers, only realizeMount, run
// after tx commits, does that. A reused record is handed back exactly
// as is, even if it may turn out not to be currently mounted:
// repairing that in place is realizeMount's job too, so that identity
// resolution here never has to wait on a filesystem check to decide
// whether to fault a record out and mint a new id in its place. A
// record is never deleted and replaced by resolving the same identity
// again: that would let two ids describe what is really one mount.
//
// Callers must have already confirmed name's activation bucket
// exists.
func resolvePosition(tx *bolt.Tx, targetsName, namespace, name string, index int, m mount.Mount, start time.Time) (mountedRecord, error) {
	v2bkt, err := tx.CreateBucketIfNotExists(bucketKeyV2)
	if err != nil {
		return mountedRecord{}, err
	}
	nsbkt, err := v2bkt.CreateBucketIfNotExists([]byte(namespace))
	if err != nil {
		return mountedRecord{}, err
	}
	mtbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyMounted)
	if err != nil {
		return mountedRecord{}, err
	}

	if index < 0 || index > 255 {
		return mountedRecord{}, fmt.Errorf("mount index %d out of range: %w", index, errdefs.ErrInvalidArgument)
	}
	mbkt := getSubBucket(nsbkt, bucketKeyMounts, []byte(name))
	if mbkt == nil {
		return mountedRecord{}, fmt.Errorf("mount %q: %w", name, errdefs.ErrNotFound)
	}
	abkt, err := mbkt.CreateBucketIfNotExists(bucketKeyActive)
	if err != nil {
		return mountedRecord{}, err
	}
	cbkt, err := abkt.CreateBucketIfNotExists([]byte{byte(index)})
	if err != nil {
		return mountedRecord{}, err
	}

	share := shareable(m)
	var identity []byte
	if share {
		identity = mountIdentity(m)
		xbkt := nsbkt.Bucket(bucketKeyMountedIndex)
		if xbkt != nil {
			if k := xbkt.Get(identity); len(k) > 0 {
				if bkt := mtbkt.Bucket(k); bkt != nil {
					id, _ := binary.Uvarint(k)
					existing, err := readMountedRecord(id, bkt)
					if err != nil {
						return mountedRecord{}, err
					}
					usedbybkt, err := bkt.CreateBucketIfNotExists(bucketKeyUsedBy)
					if err != nil {
						return mountedRecord{}, err
					}
					if err := usedbybkt.Put([]byte(name), nil); err != nil {
						return mountedRecord{}, err
					}
					if err := cbkt.Put(bucketKeyUses, k); err != nil {
						return mountedRecord{}, err
					}
					return existing, nil
				}
				// The index points at a record which no longer
				// exists. release always clears both together, so
				// this should not happen; tolerate it defensively by
				// dropping the stale index entry and falling through
				// to create a fresh record.
				if err := xbkt.Delete(identity); err != nil {
					return mountedRecord{}, err
				}
			}
		}
	}

	id, err := v2bkt.NextSequence()
	if err != nil {
		return mountedRecord{}, err
	}
	key := mountedKey(id)
	bkt, err := mtbkt.CreateBucket(key)
	if err != nil {
		return mountedRecord{}, err
	}
	point := filepath.Join(targetsName, backingDir, strconv.FormatUint(id, 10), mountPointName)
	if err := putMountedRecord(bkt, m, point, start); err != nil {
		return mountedRecord{}, err
	}
	usedbybkt, err := bkt.CreateBucket(bucketKeyUsedBy)
	if err != nil {
		return mountedRecord{}, err
	}
	if err := usedbybkt.Put([]byte(name), nil); err != nil {
		return mountedRecord{}, err
	}
	if share {
		xbkt, err := nsbkt.CreateBucketIfNotExists(bucketKeyMountedIndex)
		if err != nil {
			return mountedRecord{}, err
		}
		if err := xbkt.Put(identity, key); err != nil {
			return mountedRecord{}, err
		}
	}
	if err := cbkt.Put(bucketKeyUses, key); err != nil {
		return mountedRecord{}, err
	}

	return mountedRecord{id: id, mount: m, point: point, at: &start}, nil
}

// releaseMountedRecords drops the named activation's references from
// the given mounted records and returns those which lost their last
// reference. Released records are removed from the database and
// returned to the caller for unmounting, ordered so that a mount built
// on another is unmounted before the mount it was built on.
func releaseMountedRecords(tx *bolt.Tx, namespace, name string, ids []uint64) ([]mountedRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	nsbkt := getBucket(tx, bucketKeyV2, []byte(namespace))
	if nsbkt == nil {
		return nil, nil
	}
	mtbkt := nsbkt.Bucket(bucketKeyMounted)
	if mtbkt == nil {
		return nil, nil
	}
	xbkt := nsbkt.Bucket(bucketKeyMountedIndex)

	var released []mountedRecord
	for _, id := range ids {
		if id == 0 {
			continue
		}
		key := mountedKey(id)
		bkt := mtbkt.Bucket(key)
		if bkt == nil {
			continue
		}
		if usedbybkt := bkt.Bucket(bucketKeyUsedBy); usedbybkt != nil {
			if err := usedbybkt.Delete([]byte(name)); err != nil {
				return nil, err
			}
			if k, _ := usedbybkt.Cursor().First(); k != nil {
				// Still used by another activation.
				continue
			}
		}
		b, err := readMountedRecord(id, bkt)
		if err != nil {
			return nil, err
		}
		// Only ever added to the index when shareable; skip the
		// lookup for one that was not, rather than compute a digest
		// which was never a key in it.
		if xbkt != nil && shareable(b.mount) {
			if err := xbkt.Delete(mountIdentity(b.mount)); err != nil {
				return nil, err
			}
		}
		if err := mtbkt.DeleteBucket(key); err != nil {
			return nil, err
		}
		released = append(released, b)
	}

	sortUnmountOrder(released)

	return released, nil
}

// sortUnmountOrder orders mounted records so they can be safely
// unmounted.
//
// A mount which references another mount's mount point is always
// created after the mount it depends on and therefore has a higher
// id, so unmounting in descending id order never unmounts a
// filesystem which is still underneath another.
func sortUnmountOrder(records []mountedRecord) {
	sort.Slice(records, func(a, b int) bool {
		return records[a].id > records[b].id
	})
}

// activationUses returns the ids of the mounted records an activation
// uses, in mount order.
func activationUses(bkt *bolt.Bucket) []uint64 {
	abkt := bkt.Bucket(bucketKeyActive)
	if abkt == nil {
		return nil
	}
	var ids []uint64
	abkt.ForEachBucket(func(k []byte) error {
		if v := abkt.Bucket(k).Get(bucketKeyUses); len(v) > 0 {
			id, _ := binary.Uvarint(v)
			ids = append(ids, id)
		}
		return nil
	})
	return ids
}

// mountingKey returns the lock key used to serialize resolution and
// mounting of activations which resolve to the same mount.
func mountingKey(m mount.Mount) string {
	return hex.EncodeToString(mountIdentity(m))
}
