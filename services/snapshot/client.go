package snapshot

import (
	"context"
	"io"

	snapshotapi "github.com/containerd/containerd/api/services/snapshot/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
)

// NewSnapshotterFromClient returns a new Snapshotter which communicates
// over a GRPC connection.
func NewSnapshotterFromClient(client snapshotapi.SnapshotsClient) snapshot.Snapshotter {
	return &remoteSnapshotter{
		client: client,
	}
}

type remoteSnapshotter struct {
	client snapshotapi.SnapshotsClient
}

func (r *remoteSnapshotter) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	resp, err := r.client.Stat(ctx, &snapshotapi.StatSnapshotRequest{Key: key})
	if err != nil {
		return snapshot.Info{}, errdefs.FromGRPC(err)
	}
	return toInfo(resp.Info), nil
}

func (r *remoteSnapshotter) Usage(ctx context.Context, key string) (snapshot.Usage, error) {
	resp, err := r.client.Usage(ctx, &snapshotapi.UsageRequest{Key: key})
	if err != nil {
		return snapshot.Usage{}, errdefs.FromGRPC(err)
	}
	return toUsage(resp), nil
}

func (r *remoteSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	resp, err := r.client.Mounts(ctx, &snapshotapi.MountsRequest{Key: key})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return toMounts(resp.Mounts), nil
}

func (r *remoteSnapshotter) Prepare(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	resp, err := r.client.Prepare(ctx, &snapshotapi.PrepareSnapshotRequest{Key: key, Parent: parent})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return toMounts(resp.Mounts), nil
}

func (r *remoteSnapshotter) View(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	resp, err := r.client.View(ctx, &snapshotapi.ViewSnapshotRequest{Key: key, Parent: parent})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return toMounts(resp.Mounts), nil
}

func (r *remoteSnapshotter) Commit(ctx context.Context, name, key string) error {
	_, err := r.client.Commit(ctx, &snapshotapi.CommitSnapshotRequest{
		Name: name,
		Key:  key,
	})
	return errdefs.FromGRPC(err)
}

func (r *remoteSnapshotter) Remove(ctx context.Context, key string) error {
	_, err := r.client.Remove(ctx, &snapshotapi.RemoveSnapshotRequest{Key: key})
	return errdefs.FromGRPC(err)
}

func (r *remoteSnapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	sc, err := r.client.List(ctx, &snapshotapi.ListSnapshotsRequest{})
	if err != nil {
		errdefs.FromGRPC(err)
	}
	for {
		resp, err := sc.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return errdefs.FromGRPC(err)
		}
		if resp == nil {
			return nil
		}
		for _, info := range resp.Info {
			if err := fn(ctx, toInfo(info)); err != nil {
				return err
			}
		}
	}
}

func toKind(kind snapshotapi.Kind) snapshot.Kind {
	if kind == snapshotapi.KindActive {
		return snapshot.KindActive
	}
	return snapshot.KindCommitted
}

func toInfo(info snapshotapi.Info) snapshot.Info {
	return snapshot.Info{
		Name:     info.Name,
		Parent:   info.Parent,
		Kind:     toKind(info.Kind),
		Readonly: info.Readonly,
	}
}

func toUsage(resp *snapshotapi.UsageResponse) snapshot.Usage {
	return snapshot.Usage{
		Inodes: resp.Inodes,
		Size:   resp.Size_,
	}
}

func toMounts(mm []*types.Mount) []mount.Mount {
	mounts := make([]mount.Mount, len(mm))
	for i, m := range mm {
		mounts[i] = mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		}
	}
	return mounts
}
