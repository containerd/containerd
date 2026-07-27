/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package snapshotservice

import (
	"context"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/proxy"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
)

var empty = &ptypes.Empty{}

type service struct {
	sn snapshots.Snapshotter
	snapshotsapi.UnimplementedSnapshotsServer
}

// FromSnapshotter returns a Snapshot API server from a containerd snapshotter
func FromSnapshotter(sn snapshots.Snapshotter) snapshotsapi.SnapshotsServer {
	return service{sn: sn}
}

func (s service) Prepare(ctx context.Context, pr *snapshotsapi.PrepareSnapshotRequest) (*snapshotsapi.PrepareSnapshotResponse, error) {
	var opts []snapshots.Opt
	if pr.Labels != nil {
		opts = append(opts, snapshots.WithLabels(pr.Labels))
	}
	mounts, err := s.sn.Prepare(ctx, pr.Key, pr.Parent, opts...)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &snapshotsapi.PrepareSnapshotResponse{
		Mounts: mount.ToProto(mounts),
	}, nil
}

func (s service) View(ctx context.Context, pr *snapshotsapi.ViewSnapshotRequest) (*snapshotsapi.ViewSnapshotResponse, error) {
	var opts []snapshots.Opt
	if pr.Labels != nil {
		opts = append(opts, snapshots.WithLabels(pr.Labels))
	}
	mounts, err := s.sn.View(ctx, pr.Key, pr.Parent, opts...)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &snapshotsapi.ViewSnapshotResponse{
		Mounts: mount.ToProto(mounts),
	}, nil
}

func (s service) Mounts(ctx context.Context, mr *snapshotsapi.MountsRequest) (*snapshotsapi.MountsResponse, error) {
	mounts, err := s.sn.Mounts(ctx, mr.Key)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &snapshotsapi.MountsResponse{
		Mounts: mount.ToProto(mounts),
	}, nil
}

func (s service) Commit(ctx context.Context, cr *snapshotsapi.CommitSnapshotRequest) (*ptypes.Empty, error) {
	var opts []snapshots.Opt
	if cr.Labels != nil {
		opts = append(opts, snapshots.WithLabels(cr.Labels))
	}
	if cr.Parent != "" {
		opts = append(opts, snapshots.WithParent(cr.Parent))
	}
	if err := s.sn.Commit(ctx, cr.Name, cr.Key, opts...); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return empty, nil
}

func (s service) Remove(ctx context.Context, rr *snapshotsapi.RemoveSnapshotRequest) (*ptypes.Empty, error) {
	if err := s.sn.Remove(ctx, rr.Key); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return empty, nil
}

func (s service) Stat(ctx context.Context, sr *snapshotsapi.StatSnapshotRequest) (*snapshotsapi.StatSnapshotResponse, error) {
	info, err := s.sn.Stat(ctx, sr.Key)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &snapshotsapi.StatSnapshotResponse{Info: proxy.InfoToProto(info)}, nil
}

func (s service) Update(ctx context.Context, sr *snapshotsapi.UpdateSnapshotRequest) (*snapshotsapi.UpdateSnapshotResponse, error) {
	info, err := s.sn.Update(ctx, proxy.InfoFromProto(sr.Info), sr.UpdateMask.GetPaths()...)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &snapshotsapi.UpdateSnapshotResponse{Info: proxy.InfoToProto(info)}, nil
}

func (s service) List(sr *snapshotsapi.ListSnapshotsRequest, ss snapshotsapi.Snapshots_ListServer) error {
	var (
		buffer    []*snapshotsapi.Info
		sendBlock = func(block []*snapshotsapi.Info) error {
			return ss.Send(&snapshotsapi.ListSnapshotsResponse{
				Info: block,
			})
		}
	)
	err := s.sn.Walk(ss.Context(), func(ctx context.Context, info snapshots.Info) error {
		buffer = append(buffer, proxy.InfoToProto(info))

		if len(buffer) >= 100 {
			if err := sendBlock(buffer); err != nil {
				return err
			}

			buffer = buffer[:0]
		}

		return nil
	}, sr.Filters...)
	if err != nil {
		return err
	}
	if len(buffer) > 0 {
		// Send remaining infos
		if err := sendBlock(buffer); err != nil {
			return err
		}
	}

	return nil

}

func (s service) Usage(ctx context.Context, ur *snapshotsapi.UsageRequest) (*snapshotsapi.UsageResponse, error) {
	usage, err := s.sn.Usage(ctx, ur.Key)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return &snapshotsapi.UsageResponse{
		Inodes: usage.Inodes,
		Size:   usage.Size,
	}, nil
}

func (s service) Cleanup(ctx context.Context, cr *snapshotsapi.CleanupRequest) (*ptypes.Empty, error) {
	c, ok := s.sn.(snapshots.Cleaner)
	if !ok {
		return nil, errgrpc.ToGRPCf(errdefs.ErrNotImplemented, "snapshotter does not implement Cleanup method")
	}

	if err := c.Cleanup(ctx); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}

	return empty, nil
}
