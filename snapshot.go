package containerd

import (
	"context"

	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	"github.com/containerd/containerd/mount"
	snapshotservice "github.com/containerd/containerd/services/snapshot"
	"github.com/containerd/containerd/snapshot"
)

// SnapshotService returns a snapshotter which connects to the containerd
// snapshotter through GRPC.
func (c *Client) SnapshotService() snapshot.Snapshotter {
	// If client has requests server mounts, wrap to return snapshot mounts
	snapshotter := snapshotservice.NewSnapshotterFromClient(snapshotapi.NewSnapshotClient(c.conn))
	if c.ssmount {
		snapshotter = ssmountSnapshotter{Snapshotter: snapshotter}
	}
	return snapshotter
}

type ssmountSnapshotter struct {
	snapshot.Snapshotter
}

func (s ssmountSnapshotter) mounts(key string) []mount.Mount {
	return []mount.Mount{
		{
			Source: key,
			Type:   "snapshot",
		},
	}
}

func (s ssmountSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	return s.mounts(key), nil
}

func (s ssmountSnapshotter) Prepare(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	_, err := s.Snapshotter.Prepare(ctx, key, parent)
	if err != nil {
		return nil, err
	}
	return s.mounts(key), nil
}

func (s ssmountSnapshotter) View(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	_, err := s.Snapshotter.View(ctx, key, parent)
	if err != nil {
		return nil, err
	}
	return s.mounts(key), nil
}
