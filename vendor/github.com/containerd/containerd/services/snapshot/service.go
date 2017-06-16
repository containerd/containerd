package snapshot

import (
	gocontext "context"

	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	mounttypes "github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshot"
	protoempty "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func init() {
	plugin.Register("snapshots-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return newService(ic.Snapshotter)
		},
	})
}

var empty = &protoempty.Empty{}

type service struct {
	snapshotter snapshot.Snapshotter
}

func newService(snapshotter snapshot.Snapshotter) (*service, error) {
	return &service{
		snapshotter: snapshotter,
	}, nil
}

func (s *service) Register(gs *grpc.Server) error {
	snapshotapi.RegisterSnapshotServer(gs, s)
	return nil
}

func (s *service) Prepare(ctx context.Context, pr *snapshotapi.PrepareRequest) (*snapshotapi.MountsResponse, error) {
	log.G(ctx).WithField("parent", pr.Parent).WithField("key", pr.Key).Debugf("Preparing snapshot")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := s.snapshotter.Prepare(ctx, pr.Key, pr.Parent)
	if err != nil {
		return nil, grpcError(err)
	}
	return fromMounts(mounts), nil
}

func (s *service) View(ctx context.Context, pr *snapshotapi.PrepareRequest) (*snapshotapi.MountsResponse, error) {
	log.G(ctx).WithField("parent", pr.Parent).WithField("key", pr.Key).Debugf("Preparing view snapshot")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := s.snapshotter.View(ctx, pr.Key, pr.Parent)
	if err != nil {
		return nil, grpcError(err)
	}
	return fromMounts(mounts), nil
}

func (s *service) Mounts(ctx context.Context, mr *snapshotapi.MountsRequest) (*snapshotapi.MountsResponse, error) {
	log.G(ctx).WithField("key", mr.Key).Debugf("Getting snapshot mounts")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := s.snapshotter.Mounts(ctx, mr.Key)
	if err != nil {
		return nil, grpcError(err)
	}
	return fromMounts(mounts), nil
}

func (s *service) Commit(ctx context.Context, cr *snapshotapi.CommitRequest) (*protoempty.Empty, error) {
	log.G(ctx).WithField("key", cr.Key).WithField("name", cr.Name).Debugf("Committing snapshot")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	if err := s.snapshotter.Commit(ctx, cr.Name, cr.Key); err != nil {
		return nil, grpcError(err)
	}
	return empty, nil
}

func (s *service) Remove(ctx context.Context, rr *snapshotapi.RemoveRequest) (*protoempty.Empty, error) {
	log.G(ctx).WithField("key", rr.Key).Debugf("Removing snapshot")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	if err := s.snapshotter.Remove(ctx, rr.Key); err != nil {
		return nil, grpcError(err)
	}
	return empty, nil
}

func (s *service) Stat(ctx context.Context, sr *snapshotapi.StatRequest) (*snapshotapi.StatResponse, error) {
	log.G(ctx).WithField("key", sr.Key).Debugf("Statting snapshot")
	// TODO: Apply namespace
	info, err := s.snapshotter.Stat(ctx, sr.Key)
	if err != nil {
		return nil, grpcError(err)
	}

	return &snapshotapi.StatResponse{Info: fromInfo(info)}, nil
}

func (s *service) List(sr *snapshotapi.ListRequest, ss snapshotapi.Snapshot_ListServer) error {
	// TODO: Apply namespace

	var (
		buffer    []snapshotapi.Info
		sendBlock = func(block []snapshotapi.Info) error {
			return ss.Send(&snapshotapi.ListResponse{
				Info: block,
			})
		}
	)
	err := s.snapshotter.Walk(ss.Context(), func(ctx gocontext.Context, info snapshot.Info) error {
		buffer = append(buffer, fromInfo(info))

		if len(buffer) >= 100 {
			if err := sendBlock(buffer); err != nil {
				return err
			}

			buffer = buffer[:0]
		}

		return nil
	})
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

func (s *service) Usage(ctx context.Context, ur *snapshotapi.UsageRequest) (*snapshotapi.UsageResponse, error) {
	// TODO: Apply namespace
	usage, err := s.snapshotter.Usage(ctx, ur.Key)
	if err != nil {
		return nil, grpcError(err)
	}

	return fromUsage(usage), nil
}

func grpcError(err error) error {
	if snapshot.IsNotExist(err) {
		return grpc.Errorf(codes.NotFound, err.Error())
	}
	if snapshot.IsExist(err) {
		return grpc.Errorf(codes.AlreadyExists, err.Error())
	}
	if snapshot.IsNotActive(err) || snapshot.IsNotCommitted(err) {
		return grpc.Errorf(codes.FailedPrecondition, err.Error())
	}

	return err
}

func fromKind(kind snapshot.Kind) snapshotapi.Kind {
	if kind == snapshot.KindActive {
		return snapshotapi.KindActive
	}
	return snapshotapi.KindCommitted
}

func fromInfo(info snapshot.Info) snapshotapi.Info {
	return snapshotapi.Info{
		Name:     info.Name,
		Parent:   info.Parent,
		Kind:     fromKind(info.Kind),
		Readonly: info.Readonly,
	}
}

func fromUsage(usage snapshot.Usage) *snapshotapi.UsageResponse {
	return &snapshotapi.UsageResponse{
		Inodes: usage.Inodes,
		Size_:  usage.Size,
	}
}

func fromMounts(mounts []mount.Mount) *snapshotapi.MountsResponse {
	resp := &snapshotapi.MountsResponse{
		Mounts: make([]*mounttypes.Mount, len(mounts)),
	}
	for i, m := range mounts {
		resp.Mounts[i] = &mounttypes.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		}
	}
	return resp
}
