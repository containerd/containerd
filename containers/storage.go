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
	panic("not implemented")
}

func (s *storage) List(ctx context.Context, filter string) ([]Container, error) {
	panic("not implemented")
}

func (s *storage) Create(ctx context.Context, container Container) (Container, error) {
	panic("not implemented")
}

func (s *storage) Update(ctx context.Context, container Container) (Container, error) {
	panic("not implemented")
}

func (s *storage) Delete(ctx context.Context, id string) error {
	panic("not implemented")
}
