package cimfs

import (
	"fmt"
	"strconv"

	bolt "go.etcd.io/bbolt"
)

// Persistence store structure
// v1 (bucket)
// |-- "cimmounts" (bucket)
// |-- <cimpath> (bucket) (A bucket for each cim)
// |----|--(key: "volume") (value: <volume path>)
// |----|--(key: "refcount") (value: <refcount>)

type store struct {
	db *bolt.DB
}

var (
	bucketKeyVersion   = []byte("v1")
	bucketKeyCimMounts = []byte("cimmounts")
	bucketKeyVolume    = []byte("volume")
	bucketKeyRefCount  = []byte("refcount")
)

func newStore(dbpath string) (*store, error) {
	db, err := bolt.Open(dbpath, 0666, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open db: %w", err)
	}
	s := &store{
		db: db,
	}

	// initialize the top buckets
	err = db.Update(func(tx *bolt.Tx) error {
		vbkt, err := tx.CreateBucketIfNotExists(bucketKeyVersion)
		if err != nil {
			return err
		}
		_, err = vbkt.CreateBucketIfNotExists(bucketKeyCimMounts)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("initialize top buckets: %w", err)
	}
	return s, nil
}

func (s *store) Close() {
	s.db.Close()
}

// fetchBucket fetches the bucket pointed to by the pathKeys. Bucket corresponding to the last pathKey is
// returned.
func fetchBucket(tx *bolt.Tx, pathKeys ...string) (*bolt.Bucket, error) {
	if len(pathKeys) < 1 {
		// top level bucket is always "v1", we should never delete that.
		return nil, nil
	}
	currBkt := tx.Bucket([]byte(pathKeys[0]))
	for i := 1; i < len(pathKeys) && currBkt != nil; i++ {
		currBkt = currBkt.Bucket([]byte(pathKeys[i]))
	}
	if currBkt == nil {
		return nil, bolt.ErrBucketNotFound
	}
	return currBkt, nil
}

// deleteBucket deletes the bucket pointed to by the pathKeys. Bucket corresponding to the last pathKey is
// deleted.
func (s *store) deleteBucket(tx *bolt.Tx, pathKeys ...string) error {
	if len(pathKeys) <= 1 {
		// top level bucket is always "v1", we should never delete that.
		return nil
	}
	currBkt := tx.Bucket([]byte(pathKeys[0]))
	for i := 1; i < len(pathKeys)-1 && currBkt != nil; i++ {
		currBkt = currBkt.Bucket([]byte(pathKeys[i]))
	}
	if currBkt == nil {
		return bolt.ErrBucketNotFound
	}
	return currBkt.DeleteBucket([]byte(pathKeys[len(pathKeys)-1]))
}

func (s *store) openCimBucket(tx *bolt.Tx, cimPath string) (*bolt.Bucket, error) {
	vbkt, err := tx.CreateBucketIfNotExists(bucketKeyVersion)
	if err != nil {
		return nil, err
	}
	cimmountsbkt, err := vbkt.CreateBucketIfNotExists(bucketKeyCimMounts)
	if err != nil {
		return nil, err
	}
	cimbkt, err := cimmountsbkt.CreateBucketIfNotExists([]byte(cimPath))
	if err != nil {
		return nil, err
	}
	return cimbkt, nil
}

func encodeRefCount(count uint64) []byte {
	data := fmt.Sprintf("%d", count)
	return []byte(data)
}

func decodeRefCount(data []byte) (uint64, error) {
	refCount, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return 0, err
	}
	return refCount, nil
}

type cimMountInfo struct {
	cimPath  string
	volume   string
	refCount uint64
}

// GetCimMount returns the volume at which the given cim is mounted. Returns error if cim isn't mounted.
func (s *store) getCimMountInfo(cimPath string) (*cimMountInfo, error) {
	tx, err := s.db.Begin(false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	bkt, err := fetchBucket(tx, string(bucketKeyVersion), string(bucketKeyCimMounts), cimPath)
	if err != nil {
		return nil, err
	}

	refCount, err := decodeRefCount(bkt.Get(bucketKeyRefCount))
	if err != nil {
		return nil, err
	}
	return &cimMountInfo{
		cimPath:  cimPath,
		volume:   string(bkt.Get(bucketKeyVolume)),
		refCount: refCount,
	}, nil
}

// recordCimMount adds a new entry to the db to show the given cim is mounted at given volume, if
// the cim is already mounted increments the refcount of that cim.
func (s *store) recordCimMount(cimPath, volume string) (err error) {
	tx, err := s.db.Begin(true)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	cimbkt, err := s.openCimBucket(tx, cimPath)
	if err != nil {
		return err
	}

	if err = cimbkt.Put(bucketKeyVolume, []byte(volume)); err != nil {
		return err
	}

	refCount := uint64(0)
	currRefCount := cimbkt.Get(bucketKeyRefCount)
	if currRefCount != nil {
		refCount, err = decodeRefCount(currRefCount)
		if err != nil {
			return err
		}
	}
	currRefCount = encodeRefCount(refCount + 1)
	if err = cimbkt.Put(bucketKeyRefCount, currRefCount); err != nil {
		return err
	}

	return tx.Commit()
}

// recordCimUnmount reduces the refcount of the mounted cim by 1 and removes the cim entry from the db if ref
// count is reduced to 0.
func (s *store) recordCimUnmount(cimPath string) (err error) {
	tx, err := s.db.Begin(true)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	cimbkt, err := s.openCimBucket(tx, cimPath)
	if err != nil {
		return err
	}

	currRefCount := cimbkt.Get(bucketKeyRefCount)
	if currRefCount == nil {
		return ErrCimNotMounted
	}
	refCount, err := decodeRefCount(currRefCount)
	if err != nil {
		return fmt.Errorf("decode ref count for cim: %w", err)
	}
	if refCount == 1 {
		err = s.deleteBucket(tx, string(bucketKeyVersion), string(bucketKeyCimMounts), cimPath)
	} else {
		err = cimbkt.Put(bucketKeyRefCount, encodeRefCount(refCount-1))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
