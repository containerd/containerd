package snapshot

import (
	gocontext "context"

	snapshotapi "github.com/containerd/containerd/api/services/snapshot/v1"
	"github.com/containerd/containerd/api/types/event"
	mounttypes "github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/events"
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
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "snapshots",
		Requires: []plugin.PluginType{
			plugin.SnapshotPlugin,
		},
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			e := events.GetPoster(ic.Context)
			s, err := ic.Get(plugin.SnapshotPlugin)
			if err != nil {
				return nil, err
			}
			return newService(s.(snapshot.Snapshotter), e)
		},
	})
}

var empty = &protoempty.Empty{}

type service struct {
	snapshotter snapshot.Snapshotter
	emitter     events.Poster
}

func newService(snapshotter snapshot.Snapshotter, evts events.Poster) (*service, error) {
	return &service{
		snapshotter: snapshotter,
		emitter:     evts,
	}, nil
}

func (s *service) Register(gs *grpc.Server) error {
	snapshotapi.RegisterSnapshotsServer(gs, s)
	return nil
}

func (s *service) Prepare(ctx context.Context, pr *snapshotapi.PrepareSnapshotRequest) (*snapshotapi.PrepareSnapshotResponse, error) {
	log.G(ctx).WithField("parent", pr.Parent).WithField("key", pr.Key).Debugf("Preparing snapshot")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := s.snapshotter.Prepare(ctx, pr.Key, pr.Parent)
	if err != nil {
		return nil, grpcError(err)
	}

	if err := s.emit(ctx, "/snapshot/prepare", event.SnapshotPrepare{
		Key:    pr.Key,
		Parent: pr.Parent,
	}); err != nil {
		return nil, err
	}
	return &snapshotapi.PrepareSnapshotResponse{
		Mounts: fromMounts(mounts),
	}, nil
}

func (s *service) View(ctx context.Context, pr *snapshotapi.ViewSnapshotRequest) (*snapshotapi.ViewSnapshotResponse, error) {
	log.G(ctx).WithField("parent", pr.Parent).WithField("key", pr.Key).Debugf("Preparing view snapshot")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := s.snapshotter.View(ctx, pr.Key, pr.Parent)
	if err != nil {
		return nil, grpcError(err)
	}
	return &snapshotapi.ViewSnapshotResponse{
		Mounts: fromMounts(mounts),
	}, nil
}

func (s *service) Mounts(ctx context.Context, mr *snapshotapi.MountsRequest) (*snapshotapi.MountsResponse, error) {
	log.G(ctx).WithField("key", mr.Key).Debugf("Getting snapshot mounts")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := s.snapshotter.Mounts(ctx, mr.Key)
	if err != nil {
		return nil, grpcError(err)
	}
	return &snapshotapi.MountsResponse{
		Mounts: fromMounts(mounts),
	}, nil
}

func (s *service) Commit(ctx context.Context, cr *snapshotapi.CommitSnapshotRequest) (*protoempty.Empty, error) {
	log.G(ctx).WithField("key", cr.Key).WithField("name", cr.Name).Debugf("Committing snapshot")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	if err := s.snapshotter.Commit(ctx, cr.Name, cr.Key); err != nil {
		return nil, grpcError(err)
	}

	if err := s.emit(ctx, "/snapshot/commit", event.SnapshotCommit{
		Key:  cr.Key,
		Name: cr.Name,
	}); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *service) Remove(ctx context.Context, rr *snapshotapi.RemoveSnapshotRequest) (*protoempty.Empty, error) {
	log.G(ctx).WithField("key", rr.Key).Debugf("Removing snapshot")
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	if err := s.snapshotter.Remove(ctx, rr.Key); err != nil {
		return nil, grpcError(err)
	}

	if err := s.emit(ctx, "/snapshot/remove", event.SnapshotRemove{
		Key: rr.Key,
	}); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *service) Stat(ctx context.Context, sr *snapshotapi.StatSnapshotRequest) (*snapshotapi.StatSnapshotResponse, error) {
	log.G(ctx).WithField("key", sr.Key).Debugf("Statting snapshot")
	// TODO: Apply namespace
	info, err := s.snapshotter.Stat(ctx, sr.Key)
	if err != nil {
		return nil, grpcError(err)
	}

	return &snapshotapi.StatSnapshotResponse{Info: fromInfo(info)}, nil
}

func (s *service) List(sr *snapshotapi.ListSnapshotsRequest, ss snapshotapi.Snapshots_ListServer) error {
	// TODO: Apply namespace
	var (
		buffer    []snapshotapi.Info
		sendBlock = func(block []snapshotapi.Info) error {
			return ss.Send(&snapshotapi.ListSnapshotsResponse{
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

func fromMounts(mounts []mount.Mount) []*mounttypes.Mount {
	out := make([]*mounttypes.Mount, len(mounts))
	for i, m := range mounts {
		out[i] = &mounttypes.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		}
	}
	return out
}

func (s *service) emit(ctx context.Context, topic string, evt interface{}) error {
	emitterCtx := events.WithTopic(ctx, topic)
	if err := s.emitter.Post(emitterCtx, evt); err != nil {
		return err
	}

	return nil
}
