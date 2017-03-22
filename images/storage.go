package images

import (
	"encoding/binary"
	"fmt"

	"github.com/boltdb/bolt"
	"github.com/docker/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	errImageUnknown = fmt.Errorf("image: unknown")
)

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

func InitDB(db *bolt.DB) error {
	log.L.Debug("init db")
	return db.Update(func(tx *bolt.Tx) error {
		_, err := createBucketIfNotExists(tx, bucketKeyStorageVersion, bucketKeyImages)
		return err
	})
}

func Register(tx *bolt.Tx, name string, desc ocispec.Descriptor) error {
	return withImagesBucket(tx, func(bkt *bolt.Bucket) error {
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

func Get(tx *bolt.Tx, name string) (Image, error) {
	var image Image
	if err := withImageBucket(tx, name, func(bkt *bolt.Bucket) error {
		image.Name = name
		return readImage(&image, bkt)
	}); err != nil {
		return Image{}, err
	}

	return image, nil
}

func List(tx *bolt.Tx) ([]Image, error) {
	var images []Image

	if err := withImagesBucket(tx, func(bkt *bolt.Bucket) error {
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

func readImage(image *Image, bkt *bolt.Bucket) error {
	return bkt.ForEach(func(k, v []byte) error {
		if v == nil {
			return nil // skip it? a bkt maybe?
		}

		// TODO(stevvooe): This is why we need to use byte values for
		// keys, rather than full arrays.
		switch string(k) {
		case string(bucketKeyDigest):
			image.Descriptor.Digest = digest.Digest(v)
		case string(bucketKeyMediaType):
			image.Descriptor.MediaType = string(v)
		case string(bucketKeySize):
			image.Descriptor.Size, _ = binary.Varint(v)
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
		return errImageUnknown
	}

	return fn(bkt)
}

func withImageBucket(tx *bolt.Tx, name string, fn func(bkt *bolt.Bucket) error) error {
	bkt := getImageBucket(tx, name)
	if bkt == nil {
		return errImageUnknown
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
