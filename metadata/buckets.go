package metadata

import (
	"github.com/boltdb/bolt"
)

// The layout where a "/" delineates a bucket is desribed in the following
// section. Please try to follow this as closely as possible when adding
// functionality. We can bolster this with helpers and more structure if that
// becomes an issue.
//
// Generically, we try to do the following:
//
// 	<version>/<namespace>/<object>/<key> -> <field>
//
// version: Currently, this is "v1". Additions can be made to v1 in a backwards
// compatible way. If the layout changes, a new version must be made, along
// with a migration.
//
// namespace: the namespace to which this object belongs.
//
// object: defines which object set is stored in the bucket. There are two
// special objects, "labels" and "indexes". The "labels" bucket stores the
// labels for the parent namespace. The "indexes" object is reserved for
// indexing objects, if we require in the future.
//
// key: object-specific key identifying the storage bucket for the objects
// contents.
var (
	bucketKeyVersion          = []byte("v1")
	bucketKeyObjectLabels     = []byte("labels")     // stores the labels for a namespace.
	bucketKeyObjectIndexes    = []byte("indexes")    // reserved
	bucketKeyObjectImages     = []byte("images")     // stores image objects
	bucketKeyObjectContainers = []byte("containers") // stores container objects

	bucketKeyDigest    = []byte("digest")
	bucketKeyMediaType = []byte("mediatype")
	bucketKeySize      = []byte("size")
	bucketKeyLabels    = []byte("labels")
	bucketKeyImage     = []byte("image")
	bucketKeyRuntime   = []byte("runtime")
	bucketKeySpec      = []byte("spec")
	bucketKeyRootFS    = []byte("rootfs")
	bucketKeyCreatedAt = []byte("createdat")
	bucketKeyUpdatedAt = []byte("updatedat")
)

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

func namespaceLabelsBucketPath(namespace string) [][]byte {
	return [][]byte{bucketKeyVersion, []byte(namespace), bucketKeyObjectLabels}
}

func withNamespacesLabelsBucket(tx *bolt.Tx, namespace string, fn func(bkt *bolt.Bucket) error) error {
	bkt, err := createBucketIfNotExists(tx, namespaceLabelsBucketPath(namespace)...)
	if err != nil {
		return err
	}

	return fn(bkt)
}

func getNamespaceLabelsBucket(tx *bolt.Tx, namespace string) *bolt.Bucket {
	return getBucket(tx, namespaceLabelsBucketPath(namespace)...)
}

func imagesBucketPath(namespace string) [][]byte {
	return [][]byte{bucketKeyVersion, []byte(namespace), bucketKeyObjectImages}
}

func withImagesBucket(tx *bolt.Tx, namespace string, fn func(bkt *bolt.Bucket) error) error {
	bkt, err := createBucketIfNotExists(tx, imagesBucketPath(namespace)...)
	if err != nil {
		return err
	}

	return fn(bkt)
}

func getImagesBucket(tx *bolt.Tx, namespace string) *bolt.Bucket {
	return getBucket(tx, imagesBucketPath(namespace)...)
}

func createContainersBucket(tx *bolt.Tx, namespace string) (*bolt.Bucket, error) {
	bkt, err := createBucketIfNotExists(tx, bucketKeyVersion, []byte(namespace), bucketKeyObjectContainers)
	if err != nil {
		return nil, err
	}
	return bkt, nil
}

func getContainersBucket(tx *bolt.Tx, namespace string) *bolt.Bucket {
	return getBucket(tx, bucketKeyVersion, []byte(namespace), bucketKeyObjectContainers)
}

func getContainerBucket(tx *bolt.Tx, namespace, id string) *bolt.Bucket {
	return getBucket(tx, bucketKeyVersion, []byte(namespace), bucketKeyObjectContainers, []byte(id))
}
