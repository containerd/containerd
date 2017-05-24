package containers

import (
	"context"

	"github.com/boltdb/bolt"
	"github.com/pkg/errors"
)

var (
	ErrExists   = errors.New("images: exists")
	ErrNotFound = errors.New("images: not found")
)

// IsNotFound returns true if the error is due to a missing image.
func IsNotFound(err error) bool {
	return errors.Cause(err) == ErrNotFound
}

func IsExists(err error) bool {
	return errors.Cause(err) == ErrExists
}

var (
	bucketKeyStorageVersion = []byte("v1")
	bucketKeyContainers     = []byte("containers")
	bucketKeyLabels         = []byte("labels")
	bucketKeyImage          = []byte("image")
	bucketKeyRuntime        = []byte("runtime")
	bucketKeySpec           = []byte("spec")
	bucketKeyRootFS         = []byte("rootfs")
)

type storage struct {
	tx *bolt.Tx
}

func NewStore(tx *bolt.Tx) Store {
	return &storage{
		tx: tx,
	}
}

func (s *storage) Get(ctx context.Context, id string) (Container, error) {
	bkt := getContainerBucket(s.tx, id)
	if bkt == nil {
		return Container{}, errors.Wrap(ErrNotFound, "bucket does not exist")
	}

	container := Container{ID: id}
	if err := readContainer(&container, bkt); err != nil {
		return Container{}, errors.Wrap(err, "failed to read container")
	}

	return container, nil
}

func (s *storage) List(ctx context.Context, filter string) ([]Container, error) {
	containers := []Container{}
	bkt := getContainersBucket(s.tx)
	if bkt == nil {
		return containers, nil
	}
	err := bkt.ForEach(func(k, v []byte) error {
		cbkt := bkt.Bucket(k)
		if cbkt == nil {
			return nil
		}
		container := Container{ID: string(k)}

		if err := readContainer(&container, cbkt); err != nil {
			return errors.Wrap(err, "failed to read container")
		}
		containers = append(containers, container)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return containers, nil
}

func (s *storage) Create(ctx context.Context, container Container) (Container, error) {
	bkt, err := createContainersBucket(s.tx)
	if err != nil {
		return Container{}, err
	}

	cbkt, err := bkt.CreateBucket([]byte(container.ID))
	if err != nil {
		if err == bolt.ErrBucketExists {
			err = errors.Wrap(ErrExists, "content for id already exists")
		}
		return Container{}, err
	}

	if err := writeContainer(&container, cbkt); err != nil {
		return Container{}, errors.Wrap(err, "failed to write container")
	}

	return container, nil
}

func (s *storage) Update(ctx context.Context, container Container) (Container, error) {
	bkt := getContainersBucket(s.tx)
	if bkt == nil {
		return Container{}, errors.Wrap(ErrNotFound, "no containers")
	}

	cbkt := bkt.Bucket([]byte(container.ID))
	if cbkt == nil {
		return Container{}, errors.Wrap(ErrNotFound, "no content for id")
	}

	if err := writeContainer(&container, cbkt); err != nil {
		return Container{}, errors.Wrap(err, "failed to write container")
	}

	return container, nil
}

func (s *storage) Delete(ctx context.Context, id string) error {
	bkt := getContainersBucket(s.tx)
	if bkt == nil {
		return errors.Wrap(ErrNotFound, "no containers")
	}

	err := bkt.DeleteBucket([]byte(id))
	if err == bolt.ErrBucketNotFound {
		return errors.Wrap(ErrNotFound, "no content for id")
	}
	return err
}

func readContainer(container *Container, bkt *bolt.Bucket) error {
	return bkt.ForEach(func(k, v []byte) error {
		switch string(k) {
		case string(bucketKeyImage):
			container.Image = string(v)
		case string(bucketKeyRuntime):
			container.Runtime = string(v)
		case string(bucketKeySpec):
			container.Spec = v
		case string(bucketKeyRootFS):
			container.RootFS = string(v)
		case string(bucketKeyLabels):
			lbkt := bkt.Bucket(bucketKeyLabels)
			if lbkt == nil {
				return nil
			}
			container.Labels = map[string]string{}
			if err := lbkt.ForEach(func(k, v []byte) error {
				container.Labels[string(k)] = string(v)
				return nil
			}); err != nil {
				return err
			}
		}

		return nil
	})
}

func writeContainer(container *Container, bkt *bolt.Bucket) error {
	for _, v := range [][2][]byte{
		{bucketKeyImage, []byte(container.Image)},
		{bucketKeyRuntime, []byte(container.Runtime)},
		{bucketKeySpec, container.Spec},
		{bucketKeyRootFS, []byte(container.RootFS)},
	} {
		if err := bkt.Put(v[0], v[1]); err != nil {
			return err
		}
	}
	// Remove existing labels to keep from merging
	if lbkt := bkt.Bucket(bucketKeyLabels); lbkt != nil {
		if err := bkt.DeleteBucket(bucketKeyLabels); err != nil {
			return err
		}
	}
	lbkt, err := bkt.CreateBucket(bucketKeyLabels)
	if err != nil {
		return err
	}
	for k, v := range container.Labels {
		if err := lbkt.Put([]byte(k), []byte(v)); err != nil {
			return err
		}
	}
	return nil
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
