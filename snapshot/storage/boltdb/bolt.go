package boltdb

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/boltdb/bolt"
	"github.com/docker/containerd/snapshot"
	"github.com/docker/containerd/snapshot/storage"
	"github.com/gogo/protobuf/proto"
	"github.com/pkg/errors"
)

var (
	bucketKeyStorageVersion = []byte("v1")
	bucketKeySnapshot       = []byte("snapshots")
	bucketKeyParents        = []byte("parents")
)

type boltFileTransactor struct {
	db *bolt.DB
	tx *bolt.Tx
}

type boltMetastore struct {
	dbfile string
}

// NewMetaStore returns a snapshot MetaStore for storage of metadata related to
// a snapshot driver backed by a bolt file database. This implementation is
// strongly consistent and does all metadata changes in a transaction to prevent
// against process crashes causing inconsistent metadata state.
func NewMetaStore(ctx context.Context, dbfile string) (storage.MetaStore, error) {
	return &boltMetastore{
		dbfile: dbfile,
	}, nil
}

func (ms *boltFileTransactor) Rollback() error {
	defer ms.db.Close()
	return ms.tx.Rollback()
}

func (ms *boltFileTransactor) Commit() error {
	defer ms.db.Close()
	return ms.tx.Commit()
}

type transactionKey struct{}

func (ms *boltMetastore) TransactionContext(ctx context.Context, writable bool) (context.Context, storage.Transactor, error) {
	db, err := bolt.Open(ms.dbfile, 0600, nil)
	if err != nil {
		return ctx, nil, errors.Wrap(err, "failed to open database file")
	}

	tx, err := db.Begin(writable)
	if err != nil {
		return ctx, nil, errors.Wrap(err, "failed to start transaction")
	}

	t := &boltFileTransactor{
		db: db,
		tx: tx,
	}

	ctx = context.WithValue(ctx, transactionKey{}, t)

	return ctx, t, nil
}

func (ms *boltMetastore) withBucket(ctx context.Context, fn func(ctx context.Context, tx *bolt.Bucket) error) error {
	t, ok := ctx.Value(transactionKey{}).(*boltFileTransactor)
	if !ok {
		return errors.Errorf("no transaction in context")
	}
	bkt := t.tx.Bucket(bucketKeyStorageVersion).Bucket(bucketKeySnapshot)
	return fn(ctx, bkt)
}

func (ms *boltMetastore) createBucketIfNotExists(ctx context.Context, fn func(ctx context.Context, tx *bolt.Bucket) error) error {
	t, ok := ctx.Value(transactionKey{}).(*boltFileTransactor)
	if !ok {
		return errors.Errorf("no transaction in context")
	}

	bkt, err := t.tx.CreateBucketIfNotExists(bucketKeyStorageVersion)
	if err != nil {
		return errors.Wrap(err, "failed to create version bucket")
	}
	bkt, err = bkt.CreateBucketIfNotExists(bucketKeySnapshot)
	if err != nil {
		return errors.Wrap(err, "failed to create snapshots bucket")
	}
	return fn(ctx, bkt)
}

func fromProtoKind(k Kind) snapshot.Kind {
	if k == KindActive {
		return snapshot.KindActive
	}
	return snapshot.KindCommitted
}

func toProtoKind(k snapshot.Kind) Kind {
	switch k {
	case snapshot.KindActive:
		return KindActive
	case snapshot.KindCommitted:
		return KindCommitted
	default:
		panic("unknown kind")
	}
}

func parentKey(parent, child uint64) []byte {
	b := make([]byte, binary.Size([]uint64{parent, child}))
	i := binary.PutUvarint(b, parent)
	j := binary.PutUvarint(b[i:], child)
	return b[0 : i+j]
}

func getParent(b []byte) uint64 {
	parent, _ := binary.Uvarint(b)
	return parent
}

func (ms *boltMetastore) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	var ss Snapshot
	err := ms.withBucket(ctx, func(ctx context.Context, bkt *bolt.Bucket) error {
		return getSnapshot(bkt, key, &ss)
	})
	if err != nil {
		return snapshot.Info{}, err
	}

	return snapshot.Info{
		Name:     key,
		Parent:   ss.Parent,
		Kind:     fromProtoKind(ss.Kind),
		Readonly: ss.Readonly,
	}, nil
}

func (ms *boltMetastore) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	return ms.withBucket(ctx, func(ctx context.Context, bkt *bolt.Bucket) error {
		return bkt.ForEach(func(k, v []byte) error {
			// skip nested buckets
			if v == nil {
				return nil
			}
			var ss Snapshot
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

func (ms *boltMetastore) CreateActive(ctx context.Context, key, parent string, readonly bool) (a storage.Active, err error) {
	err = ms.createBucketIfNotExists(ctx, func(ctx context.Context, bkt *bolt.Bucket) error {
		var (
			parentS *Snapshot
		)
		if parent != "" {
			parentS = new(Snapshot)
			if err := getSnapshot(bkt, parent, parentS); err != nil {
				return errors.Wrap(err, "failed to get parent snapshot")
			}

			if parentS.Kind == KindActive {
				return errors.Errorf("cannot create active from active")
			}
		}
		b := bkt.Get([]byte(key))
		if len(b) != 0 {
			return errors.Errorf("key already exists")
		}

		id, err := bkt.NextSequence()
		if err != nil {
			return errors.Wrap(err, "unable to get identifier")
		}

		ss := Snapshot{
			ID:       id,
			Parent:   parent,
			Kind:     KindActive,
			Readonly: readonly,
		}
		if err := putSnapshot(bkt, key, &ss); err != nil {
			return err
		}

		if parentS != nil {
			pbkt, err := bkt.CreateBucketIfNotExists(bucketKeyParents)
			if err != nil {
				return errors.Wrap(err, "failed to create parent bucket")
			}
			if err := pbkt.Put(parentKey(parentS.ID, ss.ID), nil); err != nil {
				return errors.Wrap(err, "failed to write parent link")
			}

			a.ParentIDs, err = ms.parents(bkt, parentS)
			if err != nil {
				return errors.Wrap(err, "failed to get parent chain")
			}
		}

		a.ID = fmt.Sprintf("%d", id)
		a.Readonly = readonly

		return nil
	})
	if err != nil {
		return storage.Active{}, err
	}

	return
}

func (ms *boltMetastore) GetActive(ctx context.Context, key string) (a storage.Active, err error) {
	err = ms.withBucket(ctx, func(ctx context.Context, bkt *bolt.Bucket) error {
		b := bkt.Get([]byte(key))
		if len(b) == 0 {
			return errors.Errorf("active not found")
		}

		var ss Snapshot
		if err := proto.Unmarshal(b, &ss); err != nil {
			return errors.Wrap(err, "failed to unmarshal snapshot")
		}
		if ss.Kind != KindActive {
			return errors.Errorf("active not found")
		}

		a.ID = fmt.Sprintf("%d", ss.ID)
		a.Readonly = ss.Readonly

		if ss.Parent != "" {
			var parent Snapshot
			if err := getSnapshot(bkt, ss.Parent, &parent); err != nil {
				return errors.Wrap(err, "failed to get parent snapshot")
			}

			a.ParentIDs, err = ms.parents(bkt, &parent)
			if err != nil {
				return errors.Wrap(err, "failed to get parent chain")
			}
		}
		return nil
	})
	if err != nil {
		return storage.Active{}, err
	}

	return
}

func (ms *boltMetastore) parents(bkt *bolt.Bucket, parent *Snapshot) (parents []string, err error) {
	for {
		parents = append(parents, fmt.Sprintf("%d", parent.ID))

		if parent.Parent == "" {
			return
		}

		var ps Snapshot
		if err := getSnapshot(bkt, parent.Parent, &ps); err != nil {
			return nil, errors.Wrap(err, "failed to get parent snapshot")
		}
		parent = &ps
	}

	return
}

func (ms *boltMetastore) Remove(ctx context.Context, key string) (id string, k snapshot.Kind, err error) {
	err = ms.withBucket(ctx, func(ctx context.Context, bkt *bolt.Bucket) error {
		var ss Snapshot
		b := bkt.Get([]byte(key))
		if len(b) == 0 {
			return errors.Errorf("key does not exist")
		}

		if err := proto.Unmarshal(b, &ss); err != nil {
			return errors.Wrap(err, "failed to unmarshal snapshot")
		}

		pbkt := bkt.Bucket(bucketKeyParents)
		if pbkt != nil {
			k, _ := pbkt.Cursor().Seek(parentKey(ss.ID, 0))
			if getParent(k) == ss.ID {
				return errors.Errorf("cannot remove snapshot with child")
			}

			if ss.Parent != "" {
				var ps Snapshot
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

func (ms *boltMetastore) Commit(ctx context.Context, key, name string) (id string, err error) {
	err = ms.withBucket(ctx, func(ctx context.Context, bkt *bolt.Bucket) error {
		b := bkt.Get([]byte(name))
		if len(b) != 0 {
			return errors.Errorf("key already exists")
		}

		var ss Snapshot
		if err := getSnapshot(bkt, key, &ss); err != nil {
			return errors.Wrap(err, "failed to get active snapshot")
		}
		if ss.Kind != KindActive {
			return errors.Errorf("key is not active snapshot")
		}
		if ss.Readonly {
			return errors.Errorf("active snapshot is readonly")
		}

		ss.Kind = KindCommitted
		ss.Readonly = true

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

func getSnapshot(bkt *bolt.Bucket, key string, ss *Snapshot) error {
	b := bkt.Get([]byte(key))
	if len(b) == 0 {
		return errors.Errorf("snapshot not found")
	}
	if err := proto.Unmarshal(b, ss); err != nil {
		return errors.Wrap(err, "failed to unmarshal snapshot")
	}
	return nil
}

func putSnapshot(bkt *bolt.Bucket, key string, ss *Snapshot) error {
	b, err := proto.Marshal(ss)
	if err != nil {
		return errors.Wrap(err, "failed to marshal snapshot")
	}

	if err := bkt.Put([]byte(key), b); err != nil {
		return errors.Wrap(err, "failed to save snapshot")
	}
	return nil
}

func getBucket(tx *bolt.Tx, keys ...[]byte) *bolt.Bucket {
	bkt := tx.Bucket(keys[0])

	for _, key := range keys[1:] {
		if bkt == nil {
			break
		}
		bkt = bkt.Bucket(key)
	}

	return bkt
}
