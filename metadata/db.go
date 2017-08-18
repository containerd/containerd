package metadata

import (
	"sync"

	"github.com/boltdb/bolt"
)

type DB struct {
	db *bolt.DB

	storeL sync.Mutex
	ss     map[string]*snapshotter
	cs     *contentStore
}

func NewDB(db *bolt.DB) *DB {
	return &DB{
		db: db,
		ss: map[string]*snapshotter{},
	}
}

func (m *DB) View(fn func(*bolt.Tx) error) error {
	return m.db.View(fn)
}

func (m *DB) Update(fn func(*bolt.Tx) error) error {
	return m.db.Update(fn)
}
