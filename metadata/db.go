package metadata

import (
	"context"
	"encoding/binary"
	"time"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshot"
	"github.com/pkg/errors"
)

const (
	// schemaVersion represents the schema version of
	// the database. This schema version represents the
	// structure of the data in the database. The schema
	// can envolve at any time but any backwards
	// incompatible changes or structural changes require
	// bumping the schema version.
	schemaVersion = "v1"

	// dbVersion represents updates to the schema
	// version which are additions and compatible with
	// prior version of the same schema.
	dbVersion = 1
)

// DB represents a metadata database backed by a bolt
// database. The database is fully namespaced and stores
// image, container, namespace, snapshot, and content data
// while proxying data shared across namespaces to backend
// datastores for content and snapshots.
type DB struct {
	db *bolt.DB
	ss map[string]snapshot.Snapshotter
	cs content.Store
}

// NewDB creates a new metadata database using the provided
// bolt database, content store, and snapshotters.
func NewDB(db *bolt.DB, cs content.Store, ss map[string]snapshot.Snapshotter) *DB {
	return &DB{
		db: db,
		ss: ss,
		cs: cs,
	}
}

// Init ensures the database is at the correct version
// and performs any needed migrations.
func (m *DB) Init(ctx context.Context) error {
	// errSkip is used when no migration or version needs to be written
	// to the database and the transaction can be immediately rolled
	// back rather than performing a much slower and unnecessary commit.
	var errSkip = errors.New("skip update")

	err := m.db.Update(func(tx *bolt.Tx) error {
		var (
			// current schema and version
			schema  = "v0"
			version = 0
		)

		// i represents the index of the first migration
		// which must be run to get the database up to date.
		// The migration's version will be checked in reverse
		// order, decrementing i for each migration which
		// represents a version newer than the current
		// database version
		i := len(migrations)

		for ; i > 0; i-- {
			migration := migrations[i-1]

			bkt := tx.Bucket([]byte(migration.schema))
			if bkt == nil {
				// Hasn't encountered another schema, go to next migration
				if schema == "v0" {
					continue
				}
				break
			}
			if schema == "v0" {
				schema = migration.schema
				vb := bkt.Get(bucketKeyDBVersion)
				if vb != nil {
					v, _ := binary.Varint(vb)
					version = int(v)
				}
			}

			if version >= migration.version {
				break
			}
		}

		// Previous version fo database found
		if schema != "v0" {
			updates := migrations[i:]

			// No migration updates, return immediately
			if len(updates) == 0 {
				return errSkip
			}

			for _, m := range updates {
				t0 := time.Now()
				if err := m.migrate(tx); err != nil {
					return errors.Wrapf(err, "failed to migrate to %s.%d", m.schema, m.version)
				}
				log.G(ctx).WithField("d", time.Now().Sub(t0)).Debugf("database migration to %s.%d finished", m.schema, m.version)
			}
		}

		bkt, err := tx.CreateBucketIfNotExists(bucketKeyVersion)
		if err != nil {
			return err
		}

		versionEncoded, err := encodeInt(dbVersion)
		if err != nil {
			return err
		}

		return bkt.Put(bucketKeyDBVersion, versionEncoded)
	})
	if err == errSkip {
		err = nil
	}
	return err
}

// ContentStore returns a namespaced content store
// proxied to a content store.
func (m *DB) ContentStore() content.Store {
	if m.cs == nil {
		return nil
	}
	return newContentStore(m, m.cs)
}

// Snapshotter returns a namespaced content store for
// the requested snapshotter name proxied to a snapshotter.
func (m *DB) Snapshotter(name string) snapshot.Snapshotter {
	sn, ok := m.ss[name]
	if !ok {
		return nil
	}
	return newSnapshotter(m, name, sn)
}

// View runs a readonly transaction on the metadata store.
func (m *DB) View(fn func(*bolt.Tx) error) error {
	return m.db.View(fn)
}

// Update runs a writable transation on the metadata store.
func (m *DB) Update(fn func(*bolt.Tx) error) error {
	return m.db.Update(fn)
}
