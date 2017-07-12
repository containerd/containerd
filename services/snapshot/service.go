package snapshot

import (
	gocontext "context"

	"github.com/boltdb/bolt"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	snapshotapi "github.com/containerd/containerd/api/services/snapshot/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshot"
	protoempty "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

type config struct {
	// Default is the default snapshotter to use for the service
	Default string `toml:"default,omitempty"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "snapshots",
		Requires: []plugin.PluginType{
			plugin.SnapshotPlugin,
			plugin.MetadataPlugin,
		},
		Config: &config{
			Default: defaultSnapshotter,
		},
		Init: newService,
	})
}

var empty = &protoempty.Empty{}

type service struct {
	snapshotters           map[string]snapshot.Snapshotter
	defaultSnapshotterName string
	emitter                events.Poster
}

func newService(ic *plugin.InitContext) (interface{}, error) {
	evts := events.GetPoster(ic.Context)
	rawSnapshotters, err := ic.GetAll(plugin.SnapshotPlugin)
	if err != nil {
		return nil, err
	}
	md, err := ic.Get(plugin.MetadataPlugin)
	if err != nil {
		return nil, err
	}
	snapshotters := make(map[string]snapshot.Snapshotter)
	for name, sn := range rawSnapshotters {
		snapshotters[name] = metadata.NewSnapshotter(md.(*bolt.DB), name, sn.(snapshot.Snapshotter))
	}

	cfg := ic.Config.(*config)
	_, ok := snapshotters[cfg.Default]
	if !ok {
		return nil, errors.Errorf("default snapshotter not loaded: %s", cfg.Default)
	}

	return &service{
		snapshotters:           snapshotters,
		defaultSnapshotterName: cfg.Default,
		emitter:                evts,
	}, nil
}

func (s *service) getSnapshotter(name string) (snapshot.Snapshotter, error) {
	if name == "" {
		name = s.defaultSnapshotterName
	}
	sn, ok := s.snapshotters[name]
	if !ok {
		return nil, errors.Errorf("snapshotter not loaded: %s", name)
	}
	return sn, nil
}

func (s *service) Register(gs *grpc.Server) error {
	snapshotapi.RegisterSnapshotsServer(gs, s)
	return nil
}

func (s *service) Prepare(ctx context.Context, pr *snapshotapi.PrepareSnapshotRequest) (*snapshotapi.PrepareSnapshotResponse, error) {
	log.G(ctx).WithField("parent", pr.Parent).WithField("key", pr.Key).Debugf("Preparing snapshot")
	sn, err := s.getSnapshotter(pr.Snapshotter)
	if err != nil {
		return nil, err
	}
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := sn.Prepare(ctx, pr.Key, pr.Parent)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	if err := s.emit(ctx, "/snapshot/prepare", &eventsapi.SnapshotPrepare{
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
	sn, err := s.getSnapshotter(pr.Snapshotter)
	if err != nil {
		return nil, err
	}
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := sn.View(ctx, pr.Key, pr.Parent)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &snapshotapi.ViewSnapshotResponse{
		Mounts: fromMounts(mounts),
	}, nil
}

func (s *service) Mounts(ctx context.Context, mr *snapshotapi.MountsRequest) (*snapshotapi.MountsResponse, error) {
	log.G(ctx).WithField("key", mr.Key).Debugf("Getting snapshot mounts")
	sn, err := s.getSnapshotter(mr.Snapshotter)
	if err != nil {
		return nil, err
	}
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	mounts, err := sn.Mounts(ctx, mr.Key)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &snapshotapi.MountsResponse{
		Mounts: fromMounts(mounts),
	}, nil
}

func (s *service) Commit(ctx context.Context, cr *snapshotapi.CommitSnapshotRequest) (*protoempty.Empty, error) {
	log.G(ctx).WithField("key", cr.Key).WithField("name", cr.Name).Debugf("Committing snapshot")
	sn, err := s.getSnapshotter(cr.Snapshotter)
	if err != nil {
		return nil, err
	}
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	if err := sn.Commit(ctx, cr.Name, cr.Key); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	if err := s.emit(ctx, "/snapshot/commit", &eventsapi.SnapshotCommit{
		Key:  cr.Key,
		Name: cr.Name,
	}); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *service) Remove(ctx context.Context, rr *snapshotapi.RemoveSnapshotRequest) (*protoempty.Empty, error) {
	log.G(ctx).WithField("key", rr.Key).Debugf("Removing snapshot")
	sn, err := s.getSnapshotter(rr.Snapshotter)
	if err != nil {
		return nil, err
	}
	// TODO: Apply namespace
	// TODO: Lookup snapshot id from metadata store
	if err := sn.Remove(ctx, rr.Key); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	if err := s.emit(ctx, "/snapshot/remove", &eventsapi.SnapshotRemove{
		Key: rr.Key,
	}); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *service) Stat(ctx context.Context, sr *snapshotapi.StatSnapshotRequest) (*snapshotapi.StatSnapshotResponse, error) {
	log.G(ctx).WithField("key", sr.Key).Debugf("Statting snapshot")
	sn, err := s.getSnapshotter(sr.Snapshotter)
	if err != nil {
		return nil, err
	}
	// TODO: Apply namespace
	info, err := sn.Stat(ctx, sr.Key)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &snapshotapi.StatSnapshotResponse{Info: fromInfo(info)}, nil
}

func (s *service) List(sr *snapshotapi.ListSnapshotsRequest, ss snapshotapi.Snapshots_ListServer) error {
	sn, err := s.getSnapshotter(sr.Snapshotter)
	if err != nil {
		return err
	}
	// TODO: Apply namespace
	var (
		buffer    []snapshotapi.Info
		sendBlock = func(block []snapshotapi.Info) error {
			return ss.Send(&snapshotapi.ListSnapshotsResponse{
				Info: block,
			})
		}
	)
	err = sn.Walk(ss.Context(), func(ctx gocontext.Context, info snapshot.Info) error {
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
	sn, err := s.getSnapshotter(ur.Snapshotter)
	if err != nil {
		return nil, err
	}
	// TODO: Apply namespace
	usage, err := sn.Usage(ctx, ur.Key)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return fromUsage(usage), nil
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

func fromMounts(mounts []mount.Mount) []*types.Mount {
	out := make([]*types.Mount, len(mounts))
	for i, m := range mounts {
		out[i] = &types.Mount{
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
