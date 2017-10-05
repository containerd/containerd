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

type DB struct {
	db *bolt.DB
	ss map[string]snapshot.Snapshotter
	cs content.Store
}

func NewDB(db *bolt.DB, cs content.Store, ss map[string]snapshot.Snapshotter) *DB {
	return &DB{
		db: db,
		ss: ss,
		cs: cs,
	}
}

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

func (m *DB) ContentStore() content.Store {
	if m.cs == nil {
		return nil
	}
	return newContentStore(m, m.cs)
}

func (m *DB) Snapshotter(name string) snapshot.Snapshotter {
	sn, ok := m.ss[name]
	if !ok {
		return nil
	}
	return newSnapshotter(m, name, sn)
}

func (m *DB) View(fn func(*bolt.Tx) error) error {
	return m.db.View(fn)
}

func (m *DB) Update(fn func(*bolt.Tx) error) error {
	return m.db.Update(fn)
}
