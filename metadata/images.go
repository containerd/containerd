package metadata

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	digest "github.com/opencontainers/go-digest"
)

type imageStore struct {
	tx *bolt.Tx
}

func NewImageStore(tx *bolt.Tx) images.Store {
	return &imageStore{tx: tx}
}

func (s *imageStore) Get(ctx context.Context, name string) (images.Image, error) {
	var image images.Image

	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return images.Image{}, err
	}

	bkt := getImagesBucket(s.tx, namespace)
	if bkt == nil {
		return images.Image{}, ErrNotFound("")
	}

	ibkt := bkt.Bucket([]byte(name))
	if ibkt == nil {
		return images.Image{}, ErrNotFound("")
	}

	image.Name = name
	if err := readImage(&image, ibkt); err != nil {
		return images.Image{}, err
	}

	return image, nil
}

func (s *imageStore) Update(ctx context.Context, image images.Image) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return withImagesBucket(s.tx, namespace, func(bkt *bolt.Bucket) error {
		ibkt, err := bkt.CreateBucketIfNotExists([]byte(image.Name))
		if err != nil {
			return err
		}

		var (
			buf         [binary.MaxVarintLen64]byte
			sizeEncoded []byte = buf[:]
		)
		sizeEncoded = sizeEncoded[:binary.PutVarint(sizeEncoded, image.Target.Size)]

		if len(sizeEncoded) == 0 {
			return fmt.Errorf("failed encoding size = %v", image.Target.Size)
		}

		for _, v := range [][2][]byte{
			{bucketKeyDigest, []byte(image.Target.Digest)},
			{bucketKeyMediaType, []byte(image.Target.MediaType)},
			{bucketKeySize, sizeEncoded},
		} {
			if err := ibkt.Put(v[0], v[1]); err != nil {
				return err
			}
		}
		// Remove existing labels to keep from merging
		if lbkt := ibkt.Bucket(bucketKeyRuntimeLabels); lbkt != nil {
			if err := ibkt.DeleteBucket(bucketKeyRuntimeLabels); err != nil {
				return err
			}
		}
		lbkt, err := ibkt.CreateBucket(bucketKeyRuntimeLabels)
		if err != nil {
			return err
		}
		for k, v := range image.Labels {
			if err := lbkt.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}

		return nil
	})

}

func (s *imageStore) List(ctx context.Context) ([]images.Image, error) {
	var m []images.Image
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	bkt := getImagesBucket(s.tx, namespace)
	if bkt == nil {
		return nil, nil // empty store
	}

	if err := bkt.ForEach(func(k, v []byte) error {
		var (
			image = images.Image{
				Name: string(k),
			}
			kbkt = bkt.Bucket(k)
		)

		if err := readImage(&image, kbkt); err != nil {
			return err
		}

		m = append(m, image)
		return nil
	}); err != nil {
		return nil, err
	}

	return m, nil
}

func (s *imageStore) Delete(ctx context.Context, name string) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return withImagesBucket(s.tx, namespace, func(bkt *bolt.Bucket) error {
		err := bkt.DeleteBucket([]byte(name))
		if err == bolt.ErrBucketNotFound {
			return ErrNotFound("")
		}
		return err
	})
}

func readImage(image *images.Image, bkt *bolt.Bucket) error {
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
		case string(bucketKeyRuntimeLabels):
			// Remove existing labels to keep from merging
			if lbkt := bkt.Bucket(bucketKeyRuntimeLabels); lbkt != nil {
				if err := bkt.DeleteBucket(bucketKeyRuntimeLabels); err != nil {
					return err
				}
			}
			lbkt := bkt.Bucket(bucketKeyRuntimeLabels)
			if lbkt == nil {
				return nil
			}
			image.Labels = map[string]string{}
			if err := lbkt.ForEach(func(k, v []byte) error {
				image.Labels[string(k)] = string(v)
				return nil
			}); err != nil {
				return err
			}
		}

		return nil
	})
}
