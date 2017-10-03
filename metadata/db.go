package metadata

import (
	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/snapshot"
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
