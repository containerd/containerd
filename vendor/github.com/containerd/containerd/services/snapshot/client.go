package snapshot

import (
	"context"
	"io"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
	"github.com/pkg/errors"
)

// NewSnapshotterFromClient returns a new Snapshotter which communicates
// over a GRPC connection.
func NewSnapshotterFromClient(client snapshotapi.SnapshotClient) snapshot.Snapshotter {
	return &remoteSnapshotter{
		client: client,
	}
}

type remoteSnapshotter struct {
	client snapshotapi.SnapshotClient
}

func (r *remoteSnapshotter) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	resp, err := r.client.Stat(ctx, &snapshotapi.StatRequest{Key: key})
	if err != nil {
		return snapshot.Info{}, rewriteGRPCError(err)
	}
	return toInfo(resp.Info), nil
}

func (r *remoteSnapshotter) Usage(ctx context.Context, key string) (snapshot.Usage, error) {
	resp, err := r.client.Usage(ctx, &snapshotapi.UsageRequest{Key: key})
	if err != nil {
		return snapshot.Usage{}, rewriteGRPCError(err)
	}
	return toUsage(resp), nil
}

func (r *remoteSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	resp, err := r.client.Mounts(ctx, &snapshotapi.MountsRequest{Key: key})
	if err != nil {
		return nil, rewriteGRPCError(err)
	}
	return toMounts(resp), nil
}

func (r *remoteSnapshotter) Prepare(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	resp, err := r.client.Prepare(ctx, &snapshotapi.PrepareRequest{Key: key, Parent: parent})
	if err != nil {
		return nil, rewriteGRPCError(err)
	}
	return toMounts(resp), nil
}

func (r *remoteSnapshotter) View(ctx context.Context, key, parent string) ([]mount.Mount, error) {
	resp, err := r.client.View(ctx, &snapshotapi.PrepareRequest{Key: key, Parent: parent})
	if err != nil {
		return nil, rewriteGRPCError(err)
	}
	return toMounts(resp), nil
}

func (r *remoteSnapshotter) Commit(ctx context.Context, name, key string) error {
	_, err := r.client.Commit(ctx, &snapshotapi.CommitRequest{
		Name: name,
		Key:  key,
	})
	return rewriteGRPCError(err)
}

func (r *remoteSnapshotter) Remove(ctx context.Context, key string) error {
	_, err := r.client.Remove(ctx, &snapshotapi.RemoveRequest{Key: key})
	return rewriteGRPCError(err)
}

func (r *remoteSnapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	sc, err := r.client.List(ctx, &snapshotapi.ListRequest{})
	if err != nil {
		rewriteGRPCError(err)
	}
	for {
		resp, err := sc.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return rewriteGRPCError(err)
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

func rewriteGRPCError(err error) error {
	switch grpc.Code(errors.Cause(err)) {
	case codes.AlreadyExists:
		return snapshot.ErrSnapshotExist
	case codes.NotFound:
		return snapshot.ErrSnapshotNotExist
	case codes.FailedPrecondition:
		desc := grpc.ErrorDesc(errors.Cause(err))
		if strings.Contains(desc, snapshot.ErrSnapshotNotActive.Error()) {
			return snapshot.ErrSnapshotNotActive
		}
		if strings.Contains(desc, snapshot.ErrSnapshotNotCommitted.Error()) {
			return snapshot.ErrSnapshotNotCommitted
		}
	}

	return err
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

func toMounts(resp *snapshotapi.MountsResponse) []mount.Mount {
	mounts := make([]mount.Mount, len(resp.Mounts))
	for i, m := range resp.Mounts {
		mounts[i] = mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		}
	}
	return mounts
}
