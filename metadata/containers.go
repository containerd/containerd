package metadata

import (
	"context"
	"time"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/pkg/errors"
)

type containerStore struct {
	tx *bolt.Tx
}

func NewContainerStore(tx *bolt.Tx) containers.Store {
	return &containerStore{
		tx: tx,
	}
}

func (s *containerStore) Get(ctx context.Context, id string) (containers.Container, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return containers.Container{}, err
	}

	bkt := getContainerBucket(s.tx, namespace, id)
	if bkt == nil {
		return containers.Container{}, errors.Wrap(ErrNotFound, "bucket does not exist")
	}

	container := containers.Container{ID: id}
	if err := readContainer(&container, bkt); err != nil {
		return containers.Container{}, errors.Wrap(err, "failed to read container")
	}

	return container, nil
}

func (s *containerStore) List(ctx context.Context, filter string) ([]containers.Container, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var (
		m   []containers.Container
		bkt = getContainersBucket(s.tx, namespace)
	)
	if bkt == nil {
		return m, nil
	}
	if err := bkt.ForEach(func(k, v []byte) error {
		cbkt := bkt.Bucket(k)
		if cbkt == nil {
			return nil
		}
		container := containers.Container{ID: string(k)}

		if err := readContainer(&container, cbkt); err != nil {
			return errors.Wrap(err, "failed to read container")
		}
		m = append(m, container)
		return nil
	}); err != nil {
		return nil, err
	}

	return m, nil
}

func (s *containerStore) Create(ctx context.Context, container containers.Container) (containers.Container, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return containers.Container{}, err
	}

	bkt, err := createContainersBucket(s.tx, namespace)
	if err != nil {
		return containers.Container{}, err
	}

	cbkt, err := bkt.CreateBucket([]byte(container.ID))
	if err != nil {
		if err == bolt.ErrBucketExists {
			err = errors.Wrap(ErrExists, "content for id already exists")
		}
		return containers.Container{}, err
	}

	container.CreatedAt = time.Now()
	container.UpdatedAt = container.CreatedAt
	if err := writeContainer(&container, cbkt); err != nil {
		return containers.Container{}, errors.Wrap(err, "failed to write container")
	}

	return container, nil
}

func (s *containerStore) Update(ctx context.Context, container containers.Container) (containers.Container, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return containers.Container{}, err
	}

	bkt := getContainersBucket(s.tx, namespace)
	if bkt == nil {
		return containers.Container{}, errors.Wrap(ErrNotFound, "no containers")
	}

	cbkt := bkt.Bucket([]byte(container.ID))
	if cbkt == nil {
		return containers.Container{}, errors.Wrap(ErrNotFound, "no content for id")
	}

	container.UpdatedAt = time.Now()
	if err := writeContainer(&container, cbkt); err != nil {
		return containers.Container{}, errors.Wrap(err, "failed to write container")
	}

	return container, nil
}

func (s *containerStore) Delete(ctx context.Context, id string) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	bkt := getContainersBucket(s.tx, namespace)
	if bkt == nil {
		return errors.Wrap(ErrNotFound, "no containers")
	}

	if err := bkt.DeleteBucket([]byte(id)); err == bolt.ErrBucketNotFound {
		return errors.Wrap(ErrNotFound, "no content for id")
	}
	return err
}

func readContainer(container *containers.Container, bkt *bolt.Bucket) error {
	return bkt.ForEach(func(k, v []byte) error {
		switch string(k) {
		case string(bucketKeyImage):
			container.Image = string(v)
		case string(bucketKeyRuntime):
			container.Runtime = string(v)
		case string(bucketKeySpec):
			container.Spec = make([]byte, len(v))
			copy(container.Spec, v)
		case string(bucketKeyRootFS):
			container.RootFS = string(v)
		case string(bucketKeyCreatedAt):
			if err := container.CreatedAt.UnmarshalBinary(v); err != nil {
				return err
			}
		case string(bucketKeyUpdatedAt):
			if err := container.UpdatedAt.UnmarshalBinary(v); err != nil {
				return err
			}
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

func writeContainer(container *containers.Container, bkt *bolt.Bucket) error {
	createdAt, err := container.CreatedAt.MarshalBinary()
	if err != nil {
		return err
	}
	updatedAt, err := container.UpdatedAt.MarshalBinary()
	if err != nil {
		return err
	}
	for _, v := range [][2][]byte{
		{bucketKeyImage, []byte(container.Image)},
		{bucketKeyRuntime, []byte(container.Runtime)},
		{bucketKeySpec, container.Spec},
		{bucketKeyRootFS, []byte(container.RootFS)},
		{bucketKeyCreatedAt, createdAt},
		{bucketKeyUpdatedAt, updatedAt},
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
