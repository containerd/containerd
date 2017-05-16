package images

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var (
	ErrExists   = errors.New("images: exists")
	ErrNotFound = errors.New("images: not found")
)

type Store interface {
	Put(ctx context.Context, name string, desc ocispec.Descriptor) error
	Get(ctx context.Context, name string) (Image, error)
	List(ctx context.Context) ([]Image, error)
	Delete(ctx context.Context, name string) error
}

// IsNotFound returns true if the error is due to a missing image.
func IsNotFound(err error) bool {
	return errors.Cause(err) == ErrNotFound
}

func IsExists(err error) bool {
	return errors.Cause(err) == ErrExists
}

var (
	bucketKeyStorageVersion = []byte("v1")
	bucketKeyImages         = []byte("images")
	bucketKeyDigest         = []byte("digest")
	bucketKeyMediaType      = []byte("mediatype")
	bucketKeySize           = []byte("size")
)

// TODO(stevvooe): This file comprises the data required to implement the
// "metadata" store. For now, it is bound tightly to the local machine and bolt
// but we can take this and use it to define a service interface.

// InitDB will initialize the database for use. The database must be opened for
// write and the caller must not be holding an open transaction.
func InitDB(db *bolt.DB) error {
	log.L.Debug("init db")
	return db.Update(func(tx *bolt.Tx) error {
		_, err := createBucketIfNotExists(tx, bucketKeyStorageVersion, bucketKeyImages)
		return err
	})
}

func NewStore(tx *bolt.Tx) Store {
	return &storage{tx: tx}
}

type storage struct {
	tx *bolt.Tx
}

func (s *storage) Get(ctx context.Context, name string) (Image, error) {
	var image Image
	if err := withImageBucket(s.tx, name, func(bkt *bolt.Bucket) error {
		image.Name = name
		return readImage(&image, bkt)
	}); err != nil {
		return Image{}, err
	}

	return image, nil
}

func (s *storage) Put(ctx context.Context, name string, desc ocispec.Descriptor) error {
	return withImagesBucket(s.tx, func(bkt *bolt.Bucket) error {
		ibkt, err := bkt.CreateBucketIfNotExists([]byte(name))
		if err != nil {
			return err
		}

		var (
			buf         [binary.MaxVarintLen64]byte
			sizeEncoded []byte = buf[:]
		)
		sizeEncoded = sizeEncoded[:binary.PutVarint(sizeEncoded, desc.Size)]

		if len(sizeEncoded) == 0 {
			return fmt.Errorf("failed encoding size = %v", desc.Size)
		}

		for _, v := range [][2][]byte{
			{bucketKeyDigest, []byte(desc.Digest)},
			{bucketKeyMediaType, []byte(desc.MediaType)},
			{bucketKeySize, sizeEncoded},
		} {
			if err := ibkt.Put(v[0], v[1]); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *storage) List(ctx context.Context) ([]Image, error) {
	var images []Image

	if err := withImagesBucket(s.tx, func(bkt *bolt.Bucket) error {
		return bkt.ForEach(func(k, v []byte) error {
			var (
				image = Image{
					Name: string(k),
				}
				kbkt = bkt.Bucket(k)
			)

			if err := readImage(&image, kbkt); err != nil {
				return err
			}

			images = append(images, image)
			return nil
		})
	}); err != nil {
		return nil, err
	}

	return images, nil
}

func (s *storage) Delete(ctx context.Context, name string) error {
	return withImagesBucket(s.tx, func(bkt *bolt.Bucket) error {
		err := bkt.DeleteBucket([]byte(name))
		if err == bolt.ErrBucketNotFound {
			return ErrNotFound
		}
		return err
	})
}

func readImage(image *Image, bkt *bolt.Bucket) error {
	return bkt.ForEach(func(k, v []byte) error {
		if v == nil {
			return nil // skip it? a bkt maybe?
		}

		// TODO(stevvooe): This is why we need to use byte values for
		// keys, rather than full arrays.
		switch string(k) {
		case string(bucketKeyDigest):
			image.Target.Digest = digest.Digest(v)
		case string(bucketKeyMediaType):
			image.Target.MediaType = string(v)
		case string(bucketKeySize):
			image.Target.Size, _ = binary.Varint(v)
		}

		return nil
	})
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
