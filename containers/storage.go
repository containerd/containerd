package containers

import (
	"context"
	"encoding/json"

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
	bkt := s.bucket()
	if bkt == nil {
		return Container{}, errors.Wrap(ErrNotFound, "bucket does not exist")
	}

	cb := bkt.Get([]byte(id))
	if len(cb) == 0 {
		return Container{}, errors.Wrap(ErrNotFound, "no content for id")
	}

	var container Container
	if err := json.Unmarshal(cb, &container); err != nil {
		return Container{}, errors.Wrap(err, "failed to unmarshal container")
	}

	return container, nil
}

func (s *storage) List(ctx context.Context, filter string) ([]Container, error) {
	containers := []Container{}
	bkt := s.bucket()
	if bkt == nil {
		return containers, nil
	}
	err := bkt.ForEach(func(k, v []byte) error {
		container := Container{ID: string(k)}
		if err := json.Unmarshal(v, &container); err != nil {
			return errors.Wrap(err, "failed to unmarshal container")
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
	bkt, err := s.createBucket()
	if err != nil {
		return Container{}, err
	}

	b := bkt.Get([]byte(container.ID))
	if len(b) > 0 {
		return Container{}, errors.Wrap(ErrExists, "content for id already exists")
	}

	b, err = json.Marshal(container)
	if err != nil {
		return Container{}, err
	}

	return container, bkt.Put([]byte(container.ID), b)
}

func (s *storage) Update(ctx context.Context, container Container) (Container, error) {
	bkt, err := s.createBucket()
	if err != nil {
		return Container{}, err
	}

	b := bkt.Get([]byte(container.ID))
	if len(b) == 0 {
		return Container{}, errors.Wrap(ErrNotFound, "no content for id")
	}

	b, err = json.Marshal(container)
	if err != nil {
		return Container{}, err
	}

	return container, bkt.Put([]byte(container.ID), b)
}

func (s *storage) Delete(ctx context.Context, id string) error {
	bkt, err := s.createBucket()
	if err != nil {
		return err
	}

	b := bkt.Get([]byte(id))
	if len(b) == 0 {
		return errors.Wrap(ErrNotFound, "no content for id")
	}

	return bkt.Delete([]byte(id))
}

func (s *storage) bucket() *bolt.Bucket {
	bkt := s.tx.Bucket(bucketKeyStorageVersion)
	if bkt == nil {
		return nil
	}
	return bkt.Bucket(bucketKeyContainers)
}

func (s *storage) createBucket() (*bolt.Bucket, error) {
	bkt, err := s.tx.CreateBucketIfNotExists(bucketKeyStorageVersion)
	if err != nil {
		return nil, err
	}
	return bkt.CreateBucketIfNotExists(bucketKeyContainers)
}
