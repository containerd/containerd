package metadata

import (
	"context"
	"fmt"
	"strings"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshot"
	"github.com/pkg/errors"
)

type snapshotter struct {
	snapshot.Snapshotter
	name string
	db   *bolt.DB
}

// NewSnapshotter returns a new Snapshotter which namespaces the given snapshot
// using the provided name and metadata store.
func NewSnapshotter(db *bolt.DB, name string, sn snapshot.Snapshotter) snapshot.Snapshotter {
	return &snapshotter{
		Snapshotter: sn,
		name:        name,
		db:          db,
	}
}

func createKey(id uint64, namespace, key string) string {
	return fmt.Sprintf("%s/%d/%s", namespace, id, key)
}

func trimKey(key string) string {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func getKey(tx *bolt.Tx, ns, name, key string) string {
	bkt := getSnapshotterBucket(tx, ns, name)
	if bkt == nil {
		return ""
	}
	v := bkt.Get([]byte(key))
	if len(v) == 0 {
		return ""
	}
	return string(v)
}

func (s *snapshotter) resolveKey(ctx context.Context, key string) (string, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return "", err
	}

	var id string
	if err := view(ctx, s.db, func(tx *bolt.Tx) error {
		id = getKey(tx, ns, s.name, key)
		if id == "" {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}
		return nil
	}); err != nil {
		return "", err
	}

	return id, nil
}

func (s *snapshotter) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	bkey, err := s.resolveKey(ctx, key)
	if err != nil {
		return snapshot.Info{}, err
	}
	info, err := s.Snapshotter.Stat(ctx, bkey)
	if err != nil {
		return snapshot.Info{}, err
	}
	info.Name = trimKey(info.Name)
	if info.Parent != "" {
		info.Parent = trimKey(info.Parent)
	}

	return info, nil
}

func (s *snapshotter) Usage(ctx context.Context, key string) (snapshot.Usage, error) {
	bkey, err := s.resolveKey(ctx, key)
	if err != nil {
		return snapshot.Usage{}, err
	}
	return s.Snapshotter.Usage(ctx, bkey)
}

func (s *snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	bkey, err := s.resolveKey(ctx, key)
	if err != nil {
		return nil, err
	}
	return s.Snapshotter.Mounts(ctx, bkey)
}

func (s *snapshotter) Prepare(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	return s.createSnapshot(ctx, key, parent, false)
}

func (s *snapshotter) View(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	return s.createSnapshot(ctx, key, parent, true)
}

func (s *snapshotter) createSnapshot(ctx context.Context, key, parent string, readonly bool) ([]mount.Mount, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var m []mount.Mount
	if err := update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt, err := createSnapshotterBucket(tx, ns, s.name)
		if err != nil {
			return err
		}

		bkey := string(bkt.Get([]byte(key)))
		if bkey != "" {
			return errors.Wrapf(errdefs.ErrAlreadyExists, "snapshot %v already exists", key)
		}
		var bparent string
		if parent != "" {
			bparent = string(bkt.Get([]byte(parent)))
			if bparent == "" {
				return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", parent)
			}
		}

		sid, err := bkt.NextSequence()
		if err != nil {
			return err
		}
		bkey = createKey(sid, ns, key)
		if err := bkt.Put([]byte(key), []byte(bkey)); err != nil {
			return err
		}

		// TODO: Consider doing this outside of transaction to lessen
		// metadata lock time
		if readonly {
			m, err = s.Snapshotter.View(ctx, bkey, bparent)
		} else {
			m, err = s.Snapshotter.Prepare(ctx, bkey, bparent)
		}
		return err
	}); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *snapshotter) Commit(ctx context.Context, name, key string) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getSnapshotterBucket(tx, ns, s.name)
		if bkt == nil {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}

		nameKey := string(bkt.Get([]byte(name)))
		if nameKey != "" {
			return errors.Wrapf(errdefs.ErrAlreadyExists, "snapshot %v already exists", name)
		}

		bkey := string(bkt.Get([]byte(key)))
		if bkey == "" {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}

		sid, err := bkt.NextSequence()
		if err != nil {
			return err
		}
		nameKey = createKey(sid, ns, name)
		if err := bkt.Put([]byte(name), []byte(nameKey)); err != nil {
			return err
		}
		if err := bkt.Delete([]byte(key)); err != nil {
			return err
		}

		// TODO: Consider doing this outside of transaction to lessen
		// metadata lock time
		return s.Snapshotter.Commit(ctx, nameKey, bkey)
	})

}

func (s *snapshotter) Remove(ctx context.Context, key string) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getSnapshotterBucket(tx, ns, s.name)
		if bkt == nil {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}

		bkey := string(bkt.Get([]byte(key)))
		if bkey == "" {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}
		if err := bkt.Delete([]byte(key)); err != nil {
			return err
		}

		return s.Snapshotter.Remove(ctx, bkey)
	})
}

func (s *snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	var keys []string

	if err := view(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getSnapshotterBucket(tx, ns, s.name)
		if bkt == nil {
			return nil
		}

		bkt.ForEach(func(k, v []byte) error {
			if len(v) > 0 {
				keys = append(keys, string(v))
			}
			return nil
		})

		return nil
	}); err != nil {
		return err
	}

	for _, k := range keys {
		info, err := s.Snapshotter.Stat(ctx, k)
		if err != nil {
			return err
		}

		info.Name = trimKey(info.Name)
		if info.Parent != "" {
			info.Parent = trimKey(info.Parent)
		}
		if err := fn(ctx, info); err != nil {
			return err
		}
	}

	return nil
}
