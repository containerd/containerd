package metadata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/metadata/boltutil"
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
	bkt = bkt.Bucket([]byte(key))
	if bkt == nil {
		return ""
	}
	v := bkt.Get(bucketKeyName)
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
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return snapshot.Info{}, err
	}

	var (
		bkey  string
		local = snapshot.Info{
			Name: key,
		}
	)
	if err := view(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getSnapshotterBucket(tx, ns, s.name)
		if bkt == nil {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}
		sbkt := bkt.Bucket([]byte(key))
		if sbkt == nil {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}
		local.Labels, err = boltutil.ReadLabels(sbkt)
		if err != nil {
			return errors.Wrap(err, "failed to read labels")
		}
		if err := boltutil.ReadTimestamps(sbkt, &local.Created, &local.Updated); err != nil {
			return errors.Wrap(err, "failed to read timestamps")
		}
		bkey = string(sbkt.Get(bucketKeyName))
		local.Parent = string(sbkt.Get(bucketKeyParent))

		return nil
	}); err != nil {
		return snapshot.Info{}, err
	}

	info, err := s.Snapshotter.Stat(ctx, bkey)
	if err != nil {
		return snapshot.Info{}, err
	}

	return overlayInfo(info, local), nil
}

func (s *snapshotter) Update(ctx context.Context, info snapshot.Info, fieldpaths ...string) (snapshot.Info, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return snapshot.Info{}, err
	}

	if info.Name == "" {
		return snapshot.Info{}, errors.Wrap(errdefs.ErrInvalidArgument, "")
	}

	var (
		bkey  string
		local = snapshot.Info{
			Name: info.Name,
		}
	)
	if err := update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getSnapshotterBucket(tx, ns, s.name)
		if bkt == nil {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", info.Name)
		}
		sbkt := bkt.Bucket([]byte(info.Name))
		if sbkt == nil {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", info.Name)
		}

		local.Labels, err = boltutil.ReadLabels(sbkt)
		if err != nil {
			return errors.Wrap(err, "failed to read labels")
		}
		if err := boltutil.ReadTimestamps(sbkt, &local.Created, &local.Updated); err != nil {
			return errors.Wrap(err, "failed to read timestamps")
		}

		// Handle field updates
		if len(fieldpaths) > 0 {
			for _, path := range fieldpaths {
				if strings.HasPrefix(path, "labels.") {
					if local.Labels == nil {
						local.Labels = map[string]string{}
					}

					key := strings.TrimPrefix(path, "labels.")
					local.Labels[key] = info.Labels[key]
					continue
				}

				switch path {
				case "labels":
					local.Labels = info.Labels
				default:
					return errors.Wrapf(errdefs.ErrInvalidArgument, "cannot update %q field on snapshot %q", path, info.Name)
				}
			}
		} else {
			local.Labels = info.Labels
		}
		local.Updated = time.Now().UTC()

		if err := boltutil.WriteTimestamps(sbkt, local.Created, local.Updated); err != nil {
			return errors.Wrap(err, "failed to read timestamps")
		}
		if err := boltutil.WriteLabels(sbkt, local.Labels); err != nil {
			return errors.Wrap(err, "failed to read labels")
		}
		bkey = string(sbkt.Get(bucketKeyName))
		local.Parent = string(sbkt.Get(bucketKeyParent))

		return nil
	}); err != nil {
		return snapshot.Info{}, err
	}

	info, err = s.Snapshotter.Stat(ctx, bkey)
	if err != nil {
		return snapshot.Info{}, err
	}

	return overlayInfo(info, local), nil
}

func overlayInfo(info, overlay snapshot.Info) snapshot.Info {
	// Merge info
	info.Name = overlay.Name
	info.Created = overlay.Created
	info.Updated = overlay.Updated
	info.Parent = overlay.Parent
	if info.Labels == nil {
		info.Labels = overlay.Labels
	} else {
		for k, v := range overlay.Labels {
			overlay.Labels[k] = v
		}
	}
	return info
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

func (s *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshot.Opt) ([]mount.Mount, error) {
	return s.createSnapshot(ctx, key, parent, false, opts)
}

func (s *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshot.Opt) ([]mount.Mount, error) {
	return s.createSnapshot(ctx, key, parent, true, opts)
}

func (s *snapshotter) createSnapshot(ctx context.Context, key, parent string, readonly bool, opts []snapshot.Opt) ([]mount.Mount, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var base snapshot.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return nil, err
		}
	}

	var m []mount.Mount
	if err := update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt, err := createSnapshotterBucket(tx, ns, s.name)
		if err != nil {
			return err
		}

		bbkt, err := bkt.CreateBucket([]byte(key))
		if err != nil {
			if err == bolt.ErrBucketExists {
				err = errors.Wrapf(errdefs.ErrAlreadyExists, "snapshot %v already exists", key)
			}
			return err
		}
		var bparent string
		if parent != "" {
			pbkt := bkt.Bucket([]byte(parent))
			if pbkt == nil {
				return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", parent)
			}
			bparent = string(pbkt.Get(bucketKeyName))

			if err := bbkt.Put(bucketKeyParent, []byte(parent)); err != nil {
				return err
			}
		}

		sid, err := bkt.NextSequence()
		if err != nil {
			return err
		}
		bkey := createKey(sid, ns, key)
		if err := bbkt.Put(bucketKeyName, []byte(bkey)); err != nil {
			return err
		}

		ts := time.Now().UTC()
		if err := boltutil.WriteTimestamps(bbkt, ts, ts); err != nil {
			return err
		}
		if err := boltutil.WriteLabels(bbkt, base.Labels); err != nil {
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

func (s *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshot.Opt) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	var base snapshot.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return err
		}
	}

	return update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getSnapshotterBucket(tx, ns, s.name)
		if bkt == nil {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}

		bbkt, err := bkt.CreateBucket([]byte(name))
		if err != nil {
			if err == bolt.ErrBucketExists {
				err = errors.Wrapf(errdefs.ErrAlreadyExists, "snapshot %v already exists", name)
			}
			return err
		}

		obkt := bkt.Bucket([]byte(key))
		if obkt == nil {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}

		bkey := string(obkt.Get(bucketKeyName))
		parent := string(obkt.Get(bucketKeyParent))

		sid, err := bkt.NextSequence()
		if err != nil {
			return err
		}

		nameKey := createKey(sid, ns, name)

		if err := bbkt.Put(bucketKeyName, []byte(nameKey)); err != nil {
			return err
		}
		if err := bbkt.Put(bucketKeyParent, []byte(parent)); err != nil {
			return err
		}
		ts := time.Now().UTC()
		if err := boltutil.WriteTimestamps(bbkt, ts, ts); err != nil {
			return err
		}
		if err := boltutil.WriteLabels(bbkt, base.Labels); err != nil {
			return err
		}
		if err := bkt.DeleteBucket([]byte(key)); err != nil {
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
		var bkey string
		bkt := getSnapshotterBucket(tx, ns, s.name)
		if bkt != nil {
			sbkt := bkt.Bucket([]byte(key))
			if sbkt != nil {
				bkey = string(sbkt.Get(bucketKeyName))
			}
		}
		if bkey == "" {
			return errors.Wrapf(errdefs.ErrNotFound, "snapshot %v does not exist", key)
		}

		if err := bkt.DeleteBucket([]byte(key)); err != nil {
			return err
		}

		return s.Snapshotter.Remove(ctx, bkey)
	})
}

type infoPair struct {
	bkey string
	info snapshot.Info
}

func (s *snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	var (
		batchSize = 100
		pairs     = []infoPair{}
		lastKey   string
	)

	for {
		if err := view(ctx, s.db, func(tx *bolt.Tx) error {
			bkt := getSnapshotterBucket(tx, ns, s.name)
			if bkt == nil {
				return nil
			}

			c := bkt.Cursor()

			var k, v []byte
			if lastKey == "" {
				k, v = c.First()
			} else {
				k, v = c.Seek([]byte(lastKey))
			}

			for k != nil {
				if v == nil {
					if len(pairs) >= batchSize {
						break
					}
					sbkt := bkt.Bucket(k)

					pair := infoPair{
						bkey: string(sbkt.Get(bucketKeyName)),
						info: snapshot.Info{
							Name:   string(k),
							Parent: string(sbkt.Get(bucketKeyParent)),
						},
					}

					err := boltutil.ReadTimestamps(sbkt, &pair.info.Created, &pair.info.Updated)
					if err != nil {
						return err
					}
					pair.info.Labels, err = boltutil.ReadLabels(sbkt)
					if err != nil {
						return err
					}

					pairs = append(pairs, pair)
				}

				k, v = c.Next()
			}

			lastKey = string(k)

			return nil
		}); err != nil {
			return err
		}

		for _, pair := range pairs {
			info, err := s.Snapshotter.Stat(ctx, pair.bkey)
			if err != nil {
				return err
			}

			if err := fn(ctx, overlayInfo(info, pair.info)); err != nil {
				return err
			}
		}

		if lastKey == "" {
			break
		}

		pairs = pairs[:0]

	}

	return nil
}
