package storage

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshot"
	db "github.com/containerd/containerd/snapshot/storage/proto"
	"github.com/gogo/protobuf/proto"
	"github.com/pkg/errors"
)

var (
	bucketKeyStorageVersion = []byte("v1")
	bucketKeySnapshot       = []byte("snapshots")
	bucketKeyParents        = []byte("parents")

	// ErrNoTransaction is returned when an operation is attempted with
	// a context which is not inside of a transaction.
	ErrNoTransaction = errors.New("no transaction in context")
)

type boltFileTransactor struct {
	db *bolt.DB
	tx *bolt.Tx
}

func (bft *boltFileTransactor) Rollback() error {
	defer bft.db.Close()
	return bft.tx.Rollback()
}

func (bft *boltFileTransactor) Commit() error {
	defer bft.db.Close()
	return bft.tx.Commit()
}

// parentKey returns a composite key of the parent and child identifiers. The
// parts of the key are separated by a zero byte.
func parentKey(parent, child uint64) []byte {
	b := make([]byte, binary.Size([]uint64{parent, child})+1)
	i := binary.PutUvarint(b, parent)
	j := binary.PutUvarint(b[i+1:], child)
	return b[0 : i+j+1]
}

// parentPrefixKey returns the parent part of the composite key with the
// zero byte separator.
func parentPrefixKey(parent uint64) []byte {
	b := make([]byte, binary.Size(parent)+1)
	i := binary.PutUvarint(b, parent)
	return b[0 : i+1]
}

// getParentPrefix returns the first part of the composite key which
// represents the parent identifier.
func getParentPrefix(b []byte) uint64 {
	parent, _ := binary.Uvarint(b)
	return parent
}

// GetInfo returns the snapshot Info directly from the metadata. Requires a
// context with a storage transaction.
func GetInfo(ctx context.Context, key string) (string, snapshot.Info, snapshot.Usage, error) {
	var ss db.Snapshot
	err := withBucket(ctx, func(ctx context.Context, bkt, pbkt *bolt.Bucket) error {
		return getSnapshot(bkt, key, &ss)
	})
	if err != nil {
		return "", snapshot.Info{}, snapshot.Usage{}, err
	}

	usage := snapshot.Usage{
		Inodes: ss.Inodes,
		Size:   ss.Size_,
	}

	return fmt.Sprint(ss.ID), snapshot.Info{
		Name:     key,
		Parent:   ss.Parent,
		Kind:     fromProtoKind(ss.Kind),
		Readonly: ss.Readonly,
	}, usage, nil
}

// WalkInfo iterates through all metadata Info for the stored snapshots and
// calls the provided function for each. Requires a context with a storage
// transaction.
func WalkInfo(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	return withBucket(ctx, func(ctx context.Context, bkt, pbkt *bolt.Bucket) error {
		return bkt.ForEach(func(k, v []byte) error {
			// skip nested buckets
			if v == nil {
				return nil
			}
			var ss db.Snapshot
			if err := proto.Unmarshal(v, &ss); err != nil {
				return errors.Wrap(err, "failed to unmarshal snapshot")
			}

			info := snapshot.Info{
				Name:     string(k),
				Parent:   ss.Parent,
				Kind:     fromProtoKind(ss.Kind),
				Readonly: ss.Readonly,
			}
			return fn(ctx, info)
		})
	})
}

// CreateActive creates a new active snapshot transaction referenced by
// the provided key. The new active snapshot will have the provided
// parent. If the readonly option is given, the active snapshot will be
// marked as readonly and can only be removed, and not committed. The
// provided context must contain a writable transaction.
func CreateActive(ctx context.Context, key, parent string, readonly bool) (a Active, err error) {
	err = createBucketIfNotExists(ctx, func(ctx context.Context, bkt, pbkt *bolt.Bucket) error {
		var (
			parentS *db.Snapshot
		)
		if parent != "" {
			parentS = new(db.Snapshot)
			if err := getSnapshot(bkt, parent, parentS); err != nil {
				return errors.Wrap(err, "failed to get parent snapshot")
			}

			if parentS.Kind != db.KindCommitted {
				return errors.Wrap(snapshot.ErrSnapshotNotCommitted, "parent is not committed snapshot")
			}
		}
		b := bkt.Get([]byte(key))
		if len(b) != 0 {
			return snapshot.ErrSnapshotExist
		}

		id, err := nextSequence(ctx)
		if err != nil {
			return errors.Wrap(err, "unable to get identifier")
		}

		ss := db.Snapshot{
			ID:       id,
			Parent:   parent,
			Kind:     db.KindActive,
			Readonly: readonly,
		}
		if err := putSnapshot(bkt, key, &ss); err != nil {
			return err
		}

		if parentS != nil {
			// Store a backlink from the key to the parent. Store the snapshot name
			// as the value to allow following the backlink to the snapshot value.
			if err := pbkt.Put(parentKey(parentS.ID, ss.ID), []byte(key)); err != nil {
				return errors.Wrap(err, "failed to write parent link")
			}

			a.ParentIDs, err = parents(bkt, parentS)
			if err != nil {
				return errors.Wrap(err, "failed to get parent chain")
			}
		}

		a.ID = fmt.Sprintf("%d", id)
		a.Readonly = readonly

		return nil
	})
	if err != nil {
		return Active{}, err
	}

	return
}

// GetActive returns the metadata for the active snapshot transaction referenced
// by the given key. Requires a context with a storage transaction.
func GetActive(ctx context.Context, key string) (a Active, err error) {
	err = withBucket(ctx, func(ctx context.Context, bkt, pbkt *bolt.Bucket) error {
		b := bkt.Get([]byte(key))
		if len(b) == 0 {
			return snapshot.ErrSnapshotNotExist
		}

		var ss db.Snapshot
		if err := proto.Unmarshal(b, &ss); err != nil {
			return errors.Wrap(err, "failed to unmarshal snapshot")
		}
		if ss.Kind != db.KindActive {
			return snapshot.ErrSnapshotNotActive
		}

		a.ID = fmt.Sprintf("%d", ss.ID)
		a.Readonly = ss.Readonly

		if ss.Parent != "" {
			var parent db.Snapshot
			if err := getSnapshot(bkt, ss.Parent, &parent); err != nil {
				return errors.Wrap(err, "failed to get parent snapshot")
			}

			a.ParentIDs, err = parents(bkt, &parent)
			if err != nil {
				return errors.Wrap(err, "failed to get parent chain")
			}
		}
		return nil
	})
	if err != nil {
		return Active{}, err
	}

	return
}

// Remove removes a snapshot from the metastore. The string identifier for the
// snapshot is returned as well as the kind. The provided context must contain a
// writable transaction.
func Remove(ctx context.Context, key string) (id string, k snapshot.Kind, err error) {
	err = withBucket(ctx, func(ctx context.Context, bkt, pbkt *bolt.Bucket) error {
		var ss db.Snapshot
		b := bkt.Get([]byte(key))
		if len(b) == 0 {
			return snapshot.ErrSnapshotNotExist
		}

		if err := proto.Unmarshal(b, &ss); err != nil {
			return errors.Wrap(err, "failed to unmarshal snapshot")
		}

		if pbkt != nil {
			k, _ := pbkt.Cursor().Seek(parentPrefixKey(ss.ID))
			if getParentPrefix(k) == ss.ID {
				return errors.Errorf("cannot remove snapshot with child")
			}

			if ss.Parent != "" {
				var ps db.Snapshot
				if err := getSnapshot(bkt, ss.Parent, &ps); err != nil {
					return errors.Wrap(err, "failed to get parent snapshot")
				}

				if err := pbkt.Delete(parentKey(ps.ID, ss.ID)); err != nil {
					return errors.Wrap(err, "failed to delte parent link")
				}
			}
		}

		if err := bkt.Delete([]byte(key)); err != nil {
			return errors.Wrap(err, "failed to delete snapshot")
		}

		id = fmt.Sprintf("%d", ss.ID)
		k = fromProtoKind(ss.Kind)

		return nil
	})

	return
}

// CommitActive renames the active snapshot transaction referenced by `key`
// as a committed snapshot referenced by `Name`. The resulting snapshot  will be
// committed and readonly. The `key` reference will no longer be available for
// lookup or removal. The returned string identifier for the committed snapshot
// is the same identifier of the original active snapshot. The provided context
// must contain a writable transaction.
func CommitActive(ctx context.Context, key, name string, usage snapshot.Usage) (id string, err error) {
	err = withBucket(ctx, func(ctx context.Context, bkt, pbkt *bolt.Bucket) error {
		b := bkt.Get([]byte(name))
		if len(b) != 0 {
			return errors.Wrap(snapshot.ErrSnapshotExist, "committed name already exists")
		}

		var ss db.Snapshot
		if err := getSnapshot(bkt, key, &ss); err != nil {
			return errors.Wrap(err, "failed to get active snapshot")
		}
		if ss.Kind != db.KindActive {
			return snapshot.ErrSnapshotNotActive
		}
		if ss.Readonly {
			return errors.Errorf("active snapshot is readonly")
		}

		ss.Kind = db.KindCommitted
		ss.Readonly = true
		ss.Inodes = usage.Inodes
		ss.Size_ = usage.Size

		if err := putSnapshot(bkt, name, &ss); err != nil {
			return err
		}
		if err := bkt.Delete([]byte(key)); err != nil {
			return errors.Wrap(err, "failed to delete active")
		}

		id = fmt.Sprintf("%d", ss.ID)

		return nil
	})
	if err != nil {
		return "", err
	}

	return
}

// nextSequence maintains the snapshot ids in the same space across namespaces
// to avoid collisions on the filesystem, which is typically not namespace
// aware. This will also be useful to ensure that snapshots can be used across
// namespaces in the future, by projecting parent relationships into an
// alternate namespace without fixing up identifiers.
func nextSequence(ctx context.Context) (uint64, error) {
	t, ok := ctx.Value(transactionKey{}).(*boltFileTransactor)
	if !ok {
		return 0, ErrNoTransaction
	}

	bkt := t.tx.Bucket(bucketKeyStorageVersion)
	if bkt == nil {
		return 0, errors.New("version bucket required for sequence")
	}

	return bkt.NextSequence()
}

func withBucket(ctx context.Context, fn func(context.Context, *bolt.Bucket, *bolt.Bucket) error) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	t, ok := ctx.Value(transactionKey{}).(*boltFileTransactor)
	if !ok {
		return ErrNoTransaction
	}
	nbkt := t.tx.Bucket(bucketKeyStorageVersion)
	if nbkt == nil {
		return errors.Wrap(snapshot.ErrSnapshotNotExist, "bucket does not exist")
	}

	bkt := nbkt.Bucket([]byte(namespace))
	if bkt == nil {
		return errors.Wrap(snapshot.ErrSnapshotNotExist, "namespace not available in snapshotter")
	}

	return fn(ctx, bkt.Bucket(bucketKeySnapshot), bkt.Bucket(bucketKeyParents))
}

func createBucketIfNotExists(ctx context.Context, fn func(context.Context, *bolt.Bucket, *bolt.Bucket) error) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	t, ok := ctx.Value(transactionKey{}).(*boltFileTransactor)
	if !ok {
		return ErrNoTransaction
	}

	nbkt, err := t.tx.CreateBucketIfNotExists(bucketKeyStorageVersion)
	if err != nil {
		return errors.Wrap(err, "failed to create version bucket")
	}

	bkt, err := nbkt.CreateBucketIfNotExists([]byte(namespace))
	if err != nil {
		return err
	}

	sbkt, err := bkt.CreateBucketIfNotExists(bucketKeySnapshot)
	if err != nil {
		return errors.Wrap(err, "failed to create snapshots bucket")
	}
	pbkt, err := bkt.CreateBucketIfNotExists(bucketKeyParents)
	if err != nil {
		return errors.Wrap(err, "failed to create snapshots bucket")
	}
	return fn(ctx, sbkt, pbkt)
}

func fromProtoKind(k db.Kind) snapshot.Kind {
	if k == db.KindActive {
		return snapshot.KindActive
	}
	return snapshot.KindCommitted
}

func parents(bkt *bolt.Bucket, parent *db.Snapshot) (parents []string, err error) {
	for {
		parents = append(parents, fmt.Sprintf("%d", parent.ID))

		if parent.Parent == "" {
			return
		}

		var ps db.Snapshot
		if err := getSnapshot(bkt, parent.Parent, &ps); err != nil {
			return nil, errors.Wrap(err, "failed to get parent snapshot")
		}
		parent = &ps
	}
}

func getSnapshot(bkt *bolt.Bucket, key string, ss *db.Snapshot) error {
	b := bkt.Get([]byte(key))
	if len(b) == 0 {
		return snapshot.ErrSnapshotNotExist
	}
	if err := proto.Unmarshal(b, ss); err != nil {
		return errors.Wrap(err, "failed to unmarshal snapshot")
	}
	return nil
}

func putSnapshot(bkt *bolt.Bucket, key string, ss *db.Snapshot) error {
	b, err := proto.Marshal(ss)
	if err != nil {
		return errors.Wrap(err, "failed to marshal snapshot")
	}

	if err := bkt.Put([]byte(key), b); err != nil {
		return errors.Wrap(err, "failed to save snapshot")
	}
	return nil
}
