package containers

import (
	"strings"

	"github.com/boltdb/bolt"
	api "github.com/containerd/containerd/api/services/containers/v1"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/filters"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "containers",
		Requires: []plugin.PluginType{
			plugin.MetadataPlugin,
		},
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			e := events.GetPoster(ic.Context)
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			return NewService(m.(*bolt.DB), e), nil
		},
	})
}

type Service struct {
	db      *bolt.DB
	emitter events.Poster
}

func NewService(db *bolt.DB, evts events.Poster) api.ContainersServer {
	return &Service{db: db, emitter: evts}
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterContainersServer(server, s)
	return nil
}

func (s *Service) Get(ctx context.Context, req *api.GetContainerRequest) (*api.GetContainerResponse, error) {
	var resp api.GetContainerResponse

	return &resp, errdefs.ToGRPC(s.withStoreView(ctx, func(ctx context.Context, store containers.Store) error {
		container, err := store.Get(ctx, req.ID)
		if err != nil {
			return err
		}
		containerpb := containerToProto(&container)
		resp.Container = containerpb

		return nil
	}))
}

func (s *Service) List(ctx context.Context, req *api.ListContainersRequest) (*api.ListContainersResponse, error) {
	var resp api.ListContainersResponse

	var fs []filters.Filter
	for _, s := range req.Filters {
		f, err := filters.Parse(s)
		if err != nil {
			return nil, grpc.Errorf(codes.InvalidArgument, err.Error())
		}

		fs = append(fs, f)
	}

	return &resp, errdefs.ToGRPC(s.withStoreView(ctx, func(ctx context.Context, store containers.Store) error {
		containers, err := store.List(ctx, fs...)
		if err != nil {
			return err
		}

		resp.Containers = containersToProto(containers)
		return nil
	}))
}

func (s *Service) Create(ctx context.Context, req *api.CreateContainerRequest) (*api.CreateContainerResponse, error) {
	var resp api.CreateContainerResponse

	if err := s.withStoreUpdate(ctx, func(ctx context.Context, store containers.Store) error {
		container := containerFromProto(&req.Container)

		created, err := store.Create(ctx, container)
		if err != nil {
			return err
		}

		resp.Container = containerToProto(&created)

		return nil
	}); err != nil {
		return &resp, errdefs.ToGRPC(err)
	}
	if err := s.emit(ctx, "/containers/create", &eventsapi.ContainerCreate{
		ID:    resp.Container.ID,
		Image: resp.Container.Image,
		Runtime: &eventsapi.ContainerCreate_Runtime{
			Name:    resp.Container.Runtime.Name,
			Options: resp.Container.Runtime.Options,
		},
	}); err != nil {
		return &resp, err
	}

	return &resp, nil
}

func (s *Service) Update(ctx context.Context, req *api.UpdateContainerRequest) (*api.UpdateContainerResponse, error) {
	var (
		resp     api.UpdateContainerResponse
		incoming = containerFromProto(&req.Container)
	)

	if err := s.withStoreUpdate(ctx, func(ctx context.Context, store containers.Store) error {

		container, err := store.Get(ctx, incoming.ID)
		if err != nil {
			return err
		}

		if container.ID != incoming.ID {
			return grpc.Errorf(codes.InvalidArgument, "container ids must match: %v != %v", container.ID, incoming.ID)
		}

		// apply the field mask. If you update this code, you better follow the
		// field mask rules in field_mask.proto. If you don't know what this
		// is, do not update this code.
		if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
			for _, path := range req.UpdateMask.Paths {
				if strings.HasPrefix(path, "labels.") {
					key := strings.TrimPrefix(path, "labels.")
					container.Labels[key] = incoming.Labels[key]
					continue
				}

				switch path {
				case "labels":
					container.Labels = incoming.Labels
				case "image":
					container.Image = incoming.Image
				case "runtime":
					// TODO(stevvooe): Should this actually be allowed?
					container.Runtime = incoming.Runtime
				case "spec":
					container.Spec = incoming.Spec
				case "rootfs":
					container.RootFS = incoming.RootFS
				default:
					return grpc.Errorf(codes.InvalidArgument, "cannot update %q field", path)
				}
			}
		} else {
			// no field mask present, just replace everything
			container = incoming
		}

		updated, err := store.Update(ctx, container)
		if err != nil {
			return err
		}

		resp.Container = containerToProto(&updated)
		return nil
	}); err != nil {
		return &resp, errdefs.ToGRPC(err)
	}

	if err := s.emit(ctx, "/containers/update", &eventsapi.ContainerUpdate{
		ID:     resp.Container.ID,
		Image:  resp.Container.Image,
		Labels: resp.Container.Labels,
		RootFS: resp.Container.RootFS,
	}); err != nil {
		return &resp, err
	}

	return &resp, nil
}

func (s *Service) Delete(ctx context.Context, req *api.DeleteContainerRequest) (*empty.Empty, error) {
	if err := s.withStoreUpdate(ctx, func(ctx context.Context, store containers.Store) error {
		return store.Delete(ctx, req.ID)
	}); err != nil {
		return &empty.Empty{}, errdefs.ToGRPC(err)
	}

	if err := s.emit(ctx, "/containers/delete", &eventsapi.ContainerDelete{
		ID: req.ID,
	}); err != nil {
		return &empty.Empty{}, err
	}

	return &empty.Empty{}, nil
}

func (s *Service) withStore(ctx context.Context, fn func(ctx context.Context, store containers.Store) error) func(tx *bolt.Tx) error {
	return func(tx *bolt.Tx) error { return fn(ctx, metadata.NewContainerStore(tx)) }
}

func (s *Service) withStoreView(ctx context.Context, fn func(ctx context.Context, store containers.Store) error) error {
	return s.db.View(s.withStore(ctx, fn))
}

func (s *Service) withStoreUpdate(ctx context.Context, fn func(ctx context.Context, store containers.Store) error) error {
	return s.db.Update(s.withStore(ctx, fn))
}

func (s *Service) emit(ctx context.Context, topic string, evt interface{}) error {
	emitterCtx := events.WithTopic(ctx, topic)
	if err := s.emitter.Post(emitterCtx, evt); err != nil {
		return err
	}

	return nil
}
