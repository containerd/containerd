package datastore

import (
	"log"

	dsq "github.com/ipfs/go-datastore/query"
)

// Here are some basic datastore implementations.

// MapDatastore uses a standard Go map for internal storage.
type MapDatastore struct {
	values map[Key][]byte
}

// NewMapDatastore constructs a MapDatastore. It is _not_ thread-safe by
// default, wrap using sync.MutexWrap if you need thread safety (the answer here
// is usually yes).
func NewMapDatastore() (d *MapDatastore) {
	return &MapDatastore{
		values: make(map[Key][]byte),
	}
}

// Put implements Datastore.Put
func (d *MapDatastore) Put(key Key, value []byte) (err error) {
	d.values[key] = value
	return nil
}

// Sync implements Datastore.Sync
func (d *MapDatastore) Sync(prefix Key) error {
	return nil
}

// Get implements Datastore.Get
func (d *MapDatastore) Get(key Key) (value []byte, err error) {
	val, found := d.values[key]
	if !found {
		return nil, ErrNotFound
	}
	return val, nil
}

// Has implements Datastore.Has
func (d *MapDatastore) Has(key Key) (exists bool, err error) {
	_, found := d.values[key]
	return found, nil
}

// GetSize implements Datastore.GetSize
func (d *MapDatastore) GetSize(key Key) (size int, err error) {
	if v, found := d.values[key]; found {
		return len(v), nil
	}
	return -1, ErrNotFound
}

// Delete implements Datastore.Delete
func (d *MapDatastore) Delete(key Key) (err error) {
	delete(d.values, key)
	return nil
}

// Query implements Datastore.Query
func (d *MapDatastore) Query(q dsq.Query) (dsq.Results, error) {
	re := make([]dsq.Entry, 0, len(d.values))
	for k, v := range d.values {
		e := dsq.Entry{Key: k.String(), Size: len(v)}
		if !q.KeysOnly {
			e.Value = v
		}
		re = append(re, e)
	}
	r := dsq.ResultsWithEntries(q, re)
	r = dsq.NaiveQueryApply(q, r)
	return r, nil
}

func (d *MapDatastore) Batch() (Batch, error) {
	return NewBasicBatch(d), nil
}

func (d *MapDatastore) Close() error {
	return nil
}

// NullDatastore stores nothing, but conforms to the API.
// Useful to test with.
type NullDatastore struct {
}

// NewNullDatastore constructs a null datastoe
func NewNullDatastore() *NullDatastore {
	return &NullDatastore{}
}

// Put implements Datastore.Put
func (d *NullDatastore) Put(key Key, value []byte) (err error) {
	return nil
}

// Sync implements Datastore.Sync
func (d *NullDatastore) Sync(prefix Key) error {
	return nil
}

// Get implements Datastore.Get
func (d *NullDatastore) Get(key Key) (value []byte, err error) {
	return nil, ErrNotFound
}

// Has implements Datastore.Has
func (d *NullDatastore) Has(key Key) (exists bool, err error) {
	return false, nil
}

// Has implements Datastore.GetSize
func (d *NullDatastore) GetSize(key Key) (size int, err error) {
	return -1, ErrNotFound
}

// Delete implements Datastore.Delete
func (d *NullDatastore) Delete(key Key) (err error) {
	return nil
}

// Query implements Datastore.Query
func (d *NullDatastore) Query(q dsq.Query) (dsq.Results, error) {
	return dsq.ResultsWithEntries(q, nil), nil
}

func (d *NullDatastore) Batch() (Batch, error) {
	return NewBasicBatch(d), nil
}

func (d *NullDatastore) Close() error {
	return nil
}

// LogDatastore logs all accesses through the datastore.
type LogDatastore struct {
	Name  string
	child Datastore
}

// Shim is a datastore which has a child.
type Shim interface {
	Datastore

	Children() []Datastore
}

// NewLogDatastore constructs a log datastore.
func NewLogDatastore(ds Datastore, name string) *LogDatastore {
	if len(name) < 1 {
		name = "LogDatastore"
	}
	return &LogDatastore{Name: name, child: ds}
}

// Children implements Shim
func (d *LogDatastore) Children() []Datastore {
	return []Datastore{d.child}
}

// Put implements Datastore.Put
func (d *LogDatastore) Put(key Key, value []byte) (err error) {
	log.Printf("%s: Put %s\n", d.Name, key)
	// log.Printf("%s: Put %s ```%s```", d.Name, key, value)
	return d.child.Put(key, value)
}

// Sync implements Datastore.Sync
func (d *LogDatastore) Sync(prefix Key) error {
	log.Printf("%s: Sync %s\n", d.Name, prefix)
	return d.child.Sync(prefix)
}

// Get implements Datastore.Get
func (d *LogDatastore) Get(key Key) (value []byte, err error) {
	log.Printf("%s: Get %s\n", d.Name, key)
	return d.child.Get(key)
}

// Has implements Datastore.Has
func (d *LogDatastore) Has(key Key) (exists bool, err error) {
	log.Printf("%s: Has %s\n", d.Name, key)
	return d.child.Has(key)
}

// GetSize implements Datastore.GetSize
func (d *LogDatastore) GetSize(key Key) (size int, err error) {
	log.Printf("%s: GetSize %s\n", d.Name, key)
	return d.child.GetSize(key)
}

// Delete implements Datastore.Delete
func (d *LogDatastore) Delete(key Key) (err error) {
	log.Printf("%s: Delete %s\n", d.Name, key)
	return d.child.Delete(key)
}

// DiskUsage implements the PersistentDatastore interface.
func (d *LogDatastore) DiskUsage() (uint64, error) {
	log.Printf("%s: DiskUsage\n", d.Name)
	return DiskUsage(d.child)
}

// Query implements Datastore.Query
func (d *LogDatastore) Query(q dsq.Query) (dsq.Results, error) {
	log.Printf("%s: Query\n", d.Name)
	log.Printf("%s: q.Prefix: %s\n", d.Name, q.Prefix)
	log.Printf("%s: q.KeysOnly: %v\n", d.Name, q.KeysOnly)
	log.Printf("%s: q.Filters: %d\n", d.Name, len(q.Filters))
	log.Printf("%s: q.Orders: %d\n", d.Name, len(q.Orders))
	log.Printf("%s: q.Offset: %d\n", d.Name, q.Offset)

	return d.child.Query(q)
}

// LogBatch logs all accesses through the batch.
type LogBatch struct {
	Name  string
	child Batch
}

func (d *LogDatastore) Batch() (Batch, error) {
	log.Printf("%s: Batch\n", d.Name)
	if bds, ok := d.child.(Batching); ok {
		b, err := bds.Batch()

		if err != nil {
			return nil, err
		}
		return &LogBatch{
			Name:  d.Name,
			child: b,
		}, nil
	}
	return nil, ErrBatchUnsupported
}

// Put implements Batch.Put
func (d *LogBatch) Put(key Key, value []byte) (err error) {
	log.Printf("%s: BatchPut %s\n", d.Name, key)
	// log.Printf("%s: Put %s ```%s```", d.Name, key, value)
	return d.child.Put(key, value)
}

// Delete implements Batch.Delete
func (d *LogBatch) Delete(key Key) (err error) {
	log.Printf("%s: BatchDelete %s\n", d.Name, key)
	return d.child.Delete(key)
}

// Commit implements Batch.Commit
func (d *LogBatch) Commit() (err error) {
	log.Printf("%s: BatchCommit\n", d.Name)
	return d.child.Commit()
}

func (d *LogDatastore) Close() error {
	log.Printf("%s: Close\n", d.Name)
	return d.child.Close()
}

func (d *LogDatastore) Check() error {
	if c, ok := d.child.(CheckedDatastore); ok {
		return c.Check()
	}
	return nil
}

func (d *LogDatastore) Scrub() error {
	if c, ok := d.child.(ScrubbedDatastore); ok {
		return c.Scrub()
	}
	return nil
}

func (d *LogDatastore) CollectGarbage() error {
	if c, ok := d.child.(GCDatastore); ok {
		return c.CollectGarbage()
	}
	return nil
}
