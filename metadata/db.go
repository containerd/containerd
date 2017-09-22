package metadata

import "github.com/boltdb/bolt"

type DB struct {
	db *bolt.DB
}

func NewDB(db *bolt.DB) *DB {
	return &DB{
		db: db,
	}
}

func (m *DB) View(fn func(*bolt.Tx) error) error {
	return m.db.View(fn)
}

func (m *DB) Update(fn func(*bolt.Tx) error) error {
	return m.db.Update(fn)
}
