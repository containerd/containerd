package boltdb

import (
	"context"
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
)

type boltFileTransactor struct {
	dbfile string
}

type boltMetastore struct {
	t BoltTransactor
}

// BoltTransactor provides a transaction context using BoltDB.
type BoltTransactor interface {
	Transact(ctx context.Context, writable bool, fn func(context.Context, *bolt.Tx) error) error
}

// NewMetaStore returns a snapshot MetaStore for storage of metadata related to
// a snapshot driver backed by a bolt file database. This implementation is
// strongly consistent and does all metadata changes in a transaction to prevent
// against process crashes causing inconsistent metadata state.
func NewMetaStore(ctx context.Context, dbfile string) (storage.MetaStore, error) {
	bt := &boltFileTransactor{
		dbfile: dbfile,
	}

	return NewMetaStoreWithTransactor(ctx, bt)
}

// NewMetaStoreWithTransactor returns a new metastore using the provided
// transactor for performing operations in transactions.
func NewMetaStoreWithTransactor(ctx context.Context, t BoltTransactor) (storage.MetaStore, error) {
	// TODO: define mechanism for migrating schema
	// TODO: add field to mark deletion to keep record of removed items

	ms := &boltMetastore{
		t: t,
	}

	return ms, nil
}

func (ms *boltFileTransactor) Transact(ctx context.Context, writable bool, fn func(context.Context, *bolt.Tx) error) error {
	// TODO: acquire lock on file

	db, err := bolt.Open(ms.dbfile, 0600, nil)
	if err != nil {
		return errors.Wrap(err, "failed to open database file")
	}

	defer db.Close()

	tx, err := db.Begin(writable)
	if err != nil {
		return errors.Wrap(err, "failed to start transaction")
	}

	if err = fn(ctx, tx); err != nil {
		if err1 := tx.Rollback(); err1 != nil {
			err = errors.Wrapf(err, "rollback failure: %v", err1)
		}
		return err
	}
	if writable {
		if err := tx.Commit(); err != nil {
			return errors.Wrap(err, "failed to commit")
		}
	} else {
		if err := tx.Rollback(); err != nil {
			return errors.Wrap(err, "failed to rollback")
		}

	}
	return nil
}

func (ms *boltMetastore) withBucket(ctx context.Context, writable bool, fn func(ctx context.Context, tx *bolt.Bucket) error) error {
	return ms.t.Transact(ctx, writable, func(ctx context.Context, tx *bolt.Tx) error {
		bkt := tx.Bucket(bucketKeyStorageVersion).Bucket(bucketKeySnapshot)
		return fn(ctx, bkt)
	})
}

func (ms *boltMetastore) createBucketIfNotExists(ctx context.Context, fn func(ctx context.Context, tx *bolt.Bucket) error) error {
	return ms.t.Transact(ctx, true, func(ctx context.Context, tx *bolt.Tx) error {
		bkt, err := tx.CreateBucketIfNotExists(bucketKeyStorageVersion)
		if err != nil {
			return errors.Wrap(err, "failed to create version bucket")
		}
		bkt, err = bkt.CreateBucketIfNotExists(bucketKeySnapshot)
		if err != nil {
			return errors.Wrap(err, "failed to create snapshots bucket")
		}
		return fn(ctx, bkt)
	})
}

func fromProtoActive(active bool) snapshot.Kind {
	if active {
		return snapshot.KindActive
	}
	return snapshot.KindCommitted
}

func (ms *boltMetastore) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	var ss Snapshot
	err := ms.withBucket(ctx, false, func(ctx context.Context, bkt *bolt.Bucket) error {
		return getSnapshot(bkt, key, &ss)
	})
	if err != nil {
		return snapshot.Info{}, err
	}

	return snapshot.Info{
		Name:     key,
		Parent:   ss.Parent,
		Kind:     fromProtoActive(ss.Active),
		Readonly: ss.Readonly,
	}, nil
}

func (ms *boltMetastore) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	return ms.withBucket(ctx, false, func(ctx context.Context, bkt *bolt.Bucket) error {
		var (
			c    = bkt.Cursor()
			ss   Snapshot
			k, v = c.First()
		)
		for len(k) > 0 {
			if err := proto.Unmarshal(v, &ss); err != nil {
				return errors.Wrap(err, "failed to unmarshal snapshot")
			}

			info := snapshot.Info{
				Name:     string(k),
				Parent:   ss.Parent,
				Kind:     fromProtoActive(ss.Active),
				Readonly: ss.Readonly,
			}
			if err := fn(ctx, info); err != nil {
				return err
			}
			k, v = c.Next()
		}

		return nil
	})
}

func (ms *boltMetastore) CreateActive(ctx context.Context, key string, opts storage.CreateActiveOpts) error {
	return ms.createBucketIfNotExists(ctx, func(ctx context.Context, bkt *bolt.Bucket) error {
		var (
			parentS *Snapshot
		)
		if opts.Parent != "" {
			parentS = new(Snapshot)
			if err := getSnapshot(bkt, opts.Parent, parentS); err != nil {
				return errors.Wrap(err, "failed to get parent snapshot")
			}

			if parentS.Active {
				return errors.Errorf("cannot create active from active")
			}
			parentS.Children = parentS.Children + 1

			if err := putSnapshot(bkt, opts.Parent, parentS); err != nil {
				return errors.Wrap(err, "failed to update parent")
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
			Parent:   opts.Parent,
			Active:   true,
			Readonly: opts.Readonly,
		}
		if err := putSnapshot(bkt, key, &ss); err != nil {
			return err
		}

		if opts.Create != nil {
			var a storage.Active
			a.ID = fmt.Sprintf("%d", id)
			a.ParentIDs, err = ms.parents(bkt, parentS)
			if err != nil {
				return errors.Wrap(err, "failed to get parent chain")
			}
			a.Readonly = opts.Readonly
			if err := opts.Create(a); err != nil {
				return errors.Wrap(err, "create callback failed")
			}
		}

		return nil
	})
}

func (ms *boltMetastore) GetActive(ctx context.Context, key string) (a storage.Active, err error) {
	err = ms.withBucket(ctx, false, func(ctx context.Context, bkt *bolt.Bucket) error {
		var a storage.Active

		b := bkt.Get([]byte(key))
		if len(b) == 0 {
			return errors.Errorf("active not found")
		}

		var ss Snapshot
		if err := proto.Unmarshal(b, &ss); err != nil {
			return errors.Wrap(err, "failed to unmarshal snapshot")
		}
		if !ss.Active {
			return errors.Errorf("active not found")
		}

		a.ID = fmt.Sprintf("%d", ss.ID)
		a.Readonly = ss.Readonly

		var parentS *Snapshot
		if ss.Parent != "" {
			parentS = new(Snapshot)
			if err := getSnapshot(bkt, ss.Parent, parentS); err != nil {
				return errors.Wrap(err, "failed to get parent snapshot")
			}
		}
		a.ParentIDs, err = ms.parents(bkt, parentS)
		if err != nil {
			return errors.Wrap(err, "failed to get parent chain")
		}
		return nil
	})
	if err != nil {
		return storage.Active{}, err
	}

	return
}

func (ms *boltMetastore) parents(bkt *bolt.Bucket, parent *Snapshot) (parents []string, err error) {
	if parent == nil {
		return
	}
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

func (ms *boltMetastore) Remove(ctx context.Context, key string, opts storage.RemoveOpts) error {
	err := ms.withBucket(ctx, true, func(ctx context.Context, bkt *bolt.Bucket) error {
		var ss Snapshot
		b := bkt.Get([]byte(key))
		if len(b) == 0 {
			return errors.Errorf("key does not exist")
		}

		if err := proto.Unmarshal(b, &ss); err != nil {
			return errors.Wrap(err, "failed to unmarshal snapshot")
		}

		if ss.Children > 0 {
			return errors.Errorf("has children: %d", ss.Children)
		}

		if ss.Parent != "" {
			var ps Snapshot
			if err := getSnapshot(bkt, ss.Parent, &ps); err != nil {
				return errors.Wrap(err, "failed to get parent snapshot")
			}

			// TODO: handle consistency error if not > 0
			ps.Children = ps.Children - 1

			if err := putSnapshot(bkt, ss.Parent, &ps); err != nil {
				return errors.Wrap(err, "failed to update parent")
			}
		}

		if err := bkt.Delete([]byte(key)); err != nil {
			return errors.Wrap(err, "failed to delete snapshot")
		}

		if opts.Cleanup != nil {
			if err := opts.Cleanup(fmt.Sprintf("%d", ss.ID), fromProtoActive(ss.Active)); err != nil {
				return errors.Wrap(err, "failed to cleanup")
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (ms *boltMetastore) Commit(ctx context.Context, key string, opts storage.CommitOpts) error {
	return ms.withBucket(ctx, true, func(ctx context.Context, bkt *bolt.Bucket) error {
		b := bkt.Get([]byte(opts.Name))
		if len(b) != 0 {
			return errors.Errorf("key already exists")
		}

		var ss Snapshot
		if err := getSnapshot(bkt, key, &ss); err != nil {
			return errors.Wrap(err, "failed to get active snapshot")
		}
		if !ss.Active {
			return errors.Errorf("key is not active snapshot")
		}
		if ss.Readonly {
			return errors.Errorf("active snapshot is readonly")
		}

		ss.Active = false
		ss.Readonly = true

		if err := putSnapshot(bkt, opts.Name, &ss); err != nil {
			return err
		}
		if err := bkt.Delete([]byte(key)); err != nil {
			return errors.Wrap(err, "failed to delete active")
		}

		if opts.Commit != nil {
			if err := opts.Commit(fmt.Sprintf("%d", ss.ID)); err != nil {
				return errors.Wrap(err, "commit callback failed")
			}
		}

		return nil
	})
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

func createBucketIfNotExists(tx *bolt.Tx, keys ...[]byte) (*bolt.Bucket, error) {
	bkt, err := tx.CreateBucketIfNotExists(keys[0])
	if err != nil {
		return nil, err
	}

	for _, key := range keys[1:] {
		bkt, err = bkt.CreateBucketIfNotExists(key)
		if err != nil {
			return nil, err
		}
	}

	return bkt, nil
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
