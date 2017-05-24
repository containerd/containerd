package containers

import (
	"github.com/boltdb/bolt"
	api "github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/plugin"
	"github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func init() {
	plugin.Register("containers-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return NewService(ic.Meta), nil
		},
	})
}

type Service struct {
	db *bolt.DB
}

func NewService(db *bolt.DB) api.ContainersServer {
	return &Service{db: db}
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterContainersServer(server, s)
	return nil
}

func (s *Service) Get(ctx context.Context, req *api.GetContainerRequest) (*api.GetContainerResponse, error) {
	var resp api.GetContainerResponse

	return &resp, s.withStoreView(ctx, func(ctx context.Context, store containers.Store) error {
		container, err := store.Get(ctx, req.ID)
		if err != nil {
			return mapGRPCError(err, req.ID)
		}
		containerpb := containerToProto(&container)
		resp.Container = containerpb
		return nil
	})
}

func (s *Service) List(ctx context.Context, req *api.ListContainersRequest) (*api.ListContainersResponse, error) {
	var resp api.ListContainersResponse

	return &resp, s.withStoreView(ctx, func(ctx context.Context, store containers.Store) error {
		containers, err := store.List(ctx, req.Filter)
		if err != nil {
			return mapGRPCError(err, "")
		}

		resp.Containers = containersToProto(containers)
		return nil
	})

}

func (s *Service) Create(ctx context.Context, req *api.CreateContainerRequest) (*api.CreateContainerResponse, error) {
	var resp api.CreateContainerResponse

	return &resp, s.withStoreUpdate(ctx, func(ctx context.Context, store containers.Store) error {
		container := containerFromProto(&req.Container)

		created, err := store.Create(ctx, container)
		if err != nil {
			return mapGRPCError(err, req.Container.ID)
		}

		resp.Container = containerToProto(&created)
		return nil
	})
}

func (s *Service) Update(ctx context.Context, req *api.UpdateContainerRequest) (*api.UpdateContainerResponse, error) {
	var resp api.UpdateContainerResponse

	return &resp, s.withStoreUpdate(ctx, func(ctx context.Context, store containers.Store) error {
		container := containerFromProto(&req.Container)

		current, err := store.Get(ctx, container.ID)
		if err != nil {
			return mapGRPCError(err, container.ID)
		}

		if current.ID != container.ID {
			return grpc.Errorf(codes.InvalidArgument, "container ids must match: %v != %v", current.ID, container.ID)
		}

		// apply the field mask. If you update this code, you better follow the
		// field mask rules in field_mask.proto. If you don't know what this
		// is, do not update this code.
		if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
			for _, path := range req.UpdateMask.Paths {
				switch path {
				case "labels":
					current.Labels = container.Labels
				case "image":
					current.Image = container.Image
				case "runtime":
					// TODO(stevvooe): Should this actually be allowed?
					current.Runtime = container.Runtime
				case "spec":
					current.Spec = container.Spec
				case "rootfs":
					current.RootFS = container.RootFS
				default:
					return grpc.Errorf(codes.InvalidArgument, "cannot update %q field", path)
				}
			}
		} else {
			// no field mask present, just replace everything
			current = container
		}

		created, err := store.Update(ctx, container)
		if err != nil {
			return mapGRPCError(err, req.Container.ID)
		}

		resp.Container = containerToProto(&created)
		return nil
	})
}

func (s *Service) Delete(ctx context.Context, req *api.DeleteContainerRequest) (*empty.Empty, error) {
	return &empty.Empty{}, s.withStoreUpdate(ctx, func(ctx context.Context, store containers.Store) error {
		return mapGRPCError(store.Delete(ctx, req.ID), req.ID)
	})
}

func (s *Service) withStore(ctx context.Context, fn func(ctx context.Context, store containers.Store) error) func(tx *bolt.Tx) error {
	return func(tx *bolt.Tx) error { return fn(ctx, containers.NewStore(tx)) }
}

func (s *Service) withStoreView(ctx context.Context, fn func(ctx context.Context, store containers.Store) error) error {
	return s.db.View(s.withStore(ctx, fn))
}

func (s *Service) withStoreUpdate(ctx context.Context, fn func(ctx context.Context, store containers.Store) error) error {
	return s.db.Update(s.withStore(ctx, fn))
}
