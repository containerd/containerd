package metadata

import (
	"context"
	"strings"
	"time"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/filters"
	"github.com/containerd/containerd/identifiers"
	"github.com/containerd/containerd/namespaces"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
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
		return containers.Container{}, errors.Wrapf(errdefs.ErrNotFound, "bucket name %q:%q", namespace, id)
	}

	container := containers.Container{ID: id}
	if err := readContainer(&container, bkt); err != nil {
		return containers.Container{}, errors.Wrapf(err, "failed to read container %v", id)
	}

	return container, nil
}

func (s *containerStore) List(ctx context.Context, fs ...filters.Filter) ([]containers.Container, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var (
		m      []containers.Container
		filter = filters.Filter(filters.Any(fs))
		bkt    = getContainersBucket(s.tx, namespace)
	)

	if len(fs) == 0 {
		filter = filters.Always
	}

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

		if filter.Match(adaptContainer(container)) {
			m = append(m, container)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return m, nil
}

func adaptContainer(o interface{}) filters.Adaptor {
	obj := o.(containers.Container)
	return filters.AdapterFunc(func(fieldpath []string) (string, bool) {
		if len(fieldpath) == 0 {
			return "", false
		}

		switch fieldpath[0] {
		case "id":
			return obj.ID, len(obj.ID) > 0
		case "runtime":
			if len(fieldpath) <= 1 {
				return "", false
			}

			switch fieldpath[1] {
			case "name":
				return obj.Runtime.Name, len(obj.Runtime.Name) > 0
			default:
				return "", false
			}
		case "image":
			return obj.Image, len(obj.Image) > 0
		case "labels":
			return checkMap(fieldpath[1:], obj.Labels)
		}

		return "", false
	})
}

func checkMap(fieldpath []string, m map[string]string) (string, bool) {
	if len(m) == 0 {
		return "", false
	}

	value, ok := m[strings.Join(fieldpath, ".")]
	return value, ok
}

func (s *containerStore) Create(ctx context.Context, container containers.Container) (containers.Container, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return containers.Container{}, err
	}

	if err := identifiers.Validate(container.ID); err != nil {
		return containers.Container{}, err
	}

	bkt, err := createContainersBucket(s.tx, namespace)
	if err != nil {
		return containers.Container{}, err
	}

	cbkt, err := bkt.CreateBucket([]byte(container.ID))
	if err != nil {
		if err == bolt.ErrBucketExists {
			err = errors.Wrapf(errdefs.ErrAlreadyExists, "content %q", container.ID)
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

	if container.ID == "" {
		return containers.Container{}, errors.Wrapf(errdefs.ErrInvalidArgument, "must specify a container id")
	}

	bkt := getContainersBucket(s.tx, namespace)
	if bkt == nil {
		return containers.Container{}, errors.Wrapf(errdefs.ErrNotFound, "container %q", container.ID)
	}

	cbkt := bkt.Bucket([]byte(container.ID))
	if cbkt == nil {
		return containers.Container{}, errors.Wrapf(errdefs.ErrNotFound, "container %q", container.ID)
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
		return errors.Wrapf(errdefs.ErrNotFound, "cannot delete container %v, bucket not present", id)
	}

	if err := bkt.DeleteBucket([]byte(id)); err == bolt.ErrBucketNotFound {
		return errors.Wrapf(errdefs.ErrNotFound, "container %v", id)
	}
	return err
}

func readContainer(container *containers.Container, bkt *bolt.Bucket) error {
	return bkt.ForEach(func(k, v []byte) error {
		switch string(k) {
		case string(bucketKeyImage):
			container.Image = string(v)
		case string(bucketKeyRuntime):
			rbkt := bkt.Bucket(bucketKeyRuntime)
			if rbkt == nil {
				return nil // skip runtime. should be an error?
			}

			n := rbkt.Get(bucketKeyName)
			if n != nil {
				container.Runtime.Name = string(n)
			}

			obkt := rbkt.Get(bucketKeyOptions)
			if obkt == nil {
				return nil
			}

			var any types.Any
			if err := proto.Unmarshal(obkt, &any); err != nil {
				return err
			}
			container.Runtime.Options = &any
		case string(bucketKeySpec):
			var any types.Any
			if err := proto.Unmarshal(v, &any); err != nil {
				return err
			}
			container.Spec = &any
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
	spec, err := container.Spec.Marshal()
	if err != nil {
		return err
	}

	for _, v := range [][2][]byte{
		{bucketKeyImage, []byte(container.Image)},
		{bucketKeySpec, spec},
		{bucketKeyRootFS, []byte(container.RootFS)},
		{bucketKeyCreatedAt, createdAt},
		{bucketKeyUpdatedAt, updatedAt},
	} {
		if err := bkt.Put(v[0], v[1]); err != nil {
			return err
		}
	}

	if rbkt := bkt.Bucket(bucketKeyRuntime); rbkt != nil {
		if err := bkt.DeleteBucket(bucketKeyRuntime); err != nil {
			return err
		}
	}

	rbkt, err := bkt.CreateBucket(bucketKeyRuntime)
	if err != nil {
		return err
	}

	if err := rbkt.Put(bucketKeyName, []byte(container.Runtime.Name)); err != nil {
		return err
	}

	obkt, err := rbkt.CreateBucket(bucketKeyOptions)
	if err != nil {
		return err
	}

	if container.Runtime.Options != nil {
		data, err := proto.Marshal(container.Runtime.Options)
		if err != nil {
			return err
		}

		if err := obkt.Put(bucketKeyOptions, data); err != nil {
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
		if v == "" {
			delete(container.Labels, k) // remove since we don't actually set it
			continue
		}

		if err := lbkt.Put([]byte(k), []byte(v)); err != nil {
			return err
		}
	}

	return nil
}
