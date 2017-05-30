package metadata

import (
	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/log"
)

var (
	bucketKeyStorageVersion = []byte("v1")
	bucketKeyImages         = []byte("images")
	bucketKeyContainers     = []byte("containers")

	bucketKeyDigest    = []byte("digest")
	bucketKeyMediaType = []byte("mediatype")
	bucketKeySize      = []byte("size")
	bucketKeyLabels    = []byte("labels")
	bucketKeyImage     = []byte("image")
	bucketKeyRuntime   = []byte("runtime")
	bucketKeySpec      = []byte("spec")
	bucketKeyRootFS    = []byte("rootfs")
)

// InitDB will initialize the database for use. The database must be opened for
// write and the caller must not be holding an open transaction.
func InitDB(db *bolt.DB) error {
	log.L.Debug("init db")
	return db.Update(func(tx *bolt.Tx) error {
		if _, err := createBucketIfNotExists(tx, bucketKeyStorageVersion, bucketKeyImages); err != nil {
			return err
		}
		if _, err := createBucketIfNotExists(tx, bucketKeyStorageVersion, bucketKeyContainers); err != nil {
			return err
		}
		return nil
	})
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

func withImagesBucket(tx *bolt.Tx, fn func(bkt *bolt.Bucket) error) error {
	bkt := getImagesBucket(tx)
	if bkt == nil {
		return ErrNotFound
	}

	return fn(bkt)
}

func withImageBucket(tx *bolt.Tx, name string, fn func(bkt *bolt.Bucket) error) error {
	bkt := getImageBucket(tx, name)
	if bkt == nil {
		return ErrNotFound
	}

	return fn(bkt)
}

func getImagesBucket(tx *bolt.Tx) *bolt.Bucket {
	return getBucket(tx, bucketKeyStorageVersion, bucketKeyImages)
}

func getImageBucket(tx *bolt.Tx, name string) *bolt.Bucket {
	return getBucket(tx, bucketKeyStorageVersion, bucketKeyImages, []byte(name))
}

func createContainersBucket(tx *bolt.Tx) (*bolt.Bucket, error) {
	bkt, err := tx.CreateBucketIfNotExists(bucketKeyStorageVersion)
	if err != nil {
		return nil, err
	}
	return bkt.CreateBucketIfNotExists(bucketKeyContainers)
}

func getContainersBucket(tx *bolt.Tx) *bolt.Bucket {
	return getBucket(tx, bucketKeyStorageVersion, bucketKeyContainers)
}

func getContainerBucket(tx *bolt.Tx, id string) *bolt.Bucket {
	return getBucket(tx, bucketKeyStorageVersion, bucketKeyContainers, []byte(id))
}
