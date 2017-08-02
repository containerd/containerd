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

func (s *containerStore) List(ctx context.Context, fs ...string) ([]containers.Container, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	filter, err := filters.ParseAll(fs...)
	if err != nil {
		return nil, errors.Wrapf(errdefs.ErrInvalidArgument, err.Error())
	}

	bkt := getContainersBucket(s.tx, namespace)
	if bkt == nil {
		return nil, nil
	}

	var m []containers.Container
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

	container.CreatedAt = time.Now().UTC()
	container.UpdatedAt = container.CreatedAt
	if err := writeContainer(cbkt, &container); err != nil {
		return containers.Container{}, errors.Wrap(err, "failed to write container")
	}

	return container, nil
}

func (s *containerStore) Update(ctx context.Context, container containers.Container, fieldpaths ...string) (containers.Container, error) {
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

	var updated containers.Container
	if err := readContainer(&updated, cbkt); err != nil {
		return updated, errors.Wrapf(err, "failed to read container from bucket")
	}
	createdat := updated.CreatedAt
	updated.ID = container.ID

	// apply the field mask. If you update this code, you better follow the
	// field mask rules in field_mask.proto. If you don't know what this
	// is, do not update this code.
	if len(fieldpaths) > 0 {
		// TODO(stevvooe): Move this logic into the store itself.
		for _, path := range fieldpaths {
			if strings.HasPrefix(path, "labels.") {
				if updated.Labels == nil {
					updated.Labels = map[string]string{}
				}
				key := strings.TrimPrefix(path, "labels.")
				updated.Labels[key] = container.Labels[key]
				continue
			}

			switch path {
			case "labels":
				updated.Labels = container.Labels
			case "image":
				updated.Image = container.Image
			case "runtime":
				// TODO(stevvooe): Should this actually be allowed?
				updated.Runtime = container.Runtime
			case "spec":
				updated.Spec = container.Spec
			case "rootfs":
				updated.RootFS = container.RootFS
			default:
				return containers.Container{}, errors.Wrapf(errdefs.ErrInvalidArgument, "cannot update %q field on %q", path, container.ID)
			}
		}
	} else {
		// no field mask present, just replace everything
		updated = container
	}

	updated.CreatedAt = createdat
	updated.UpdatedAt = time.Now().UTC()
	if err := writeContainer(cbkt, &updated); err != nil {
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

			if err := readLabels(container.Labels, lbkt); err != nil {
				return err
			}
		}

		return nil
	})
}

func writeContainer(bkt *bolt.Bucket, container *containers.Container) error {
	if err := writeTimestamps(bkt, container.CreatedAt, container.UpdatedAt); err != nil {
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

	return writeLabels(bkt, container.Labels)
}
