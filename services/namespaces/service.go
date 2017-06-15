package namespaces

import (
	"strings"

	"github.com/boltdb/bolt"
	api "github.com/containerd/containerd/api/services/namespaces"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	"github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "namespaces",
		Requires: []plugin.PluginType{
			plugin.MetadataPlugin,
		},
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			return NewService(m.(*bolt.DB)), nil
		},
	})
}

type Service struct {
	db *bolt.DB
}

var _ api.NamespacesServer = &Service{}

func NewService(db *bolt.DB) api.NamespacesServer {
	return &Service{db: db}
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterNamespacesServer(server, s)
	return nil
}

func (s *Service) Get(ctx context.Context, req *api.GetNamespaceRequest) (*api.GetNamespaceResponse, error) {
	var resp api.GetNamespaceResponse

	return &resp, s.withStoreView(ctx, func(ctx context.Context, store namespaces.Store) error {
		labels, err := store.Labels(ctx, req.Name)
		if err != nil {
			return mapGRPCError(err, req.Name)
		}

		resp.Namespace = api.Namespace{
			Name:   req.Name,
			Labels: labels,
		}

		return nil
	})
}

func (s *Service) List(ctx context.Context, req *api.ListNamespacesRequest) (*api.ListNamespacesResponse, error) {
	var resp api.ListNamespacesResponse

	return &resp, s.withStoreView(ctx, func(ctx context.Context, store namespaces.Store) error {
		namespaces, err := store.List(ctx)
		if err != nil {
			return err
		}

		for _, namespace := range namespaces {
			labels, err := store.Labels(ctx, namespace)
			if err != nil {
				// In general, this should be unlikely, since we are holding a
				// transaction to service this request.
				return mapGRPCError(err, namespace)
			}

			resp.Namespaces = append(resp.Namespaces, api.Namespace{
				Name:   namespace,
				Labels: labels,
			})
		}

		return nil
	})
}

func (s *Service) Create(ctx context.Context, req *api.CreateNamespaceRequest) (*api.CreateNamespaceResponse, error) {
	var resp api.CreateNamespaceResponse

	return &resp, s.withStoreUpdate(ctx, func(ctx context.Context, store namespaces.Store) error {
		if err := store.Create(ctx, req.Namespace.Name, req.Namespace.Labels); err != nil {
			return mapGRPCError(err, req.Namespace.Name)
		}

		for k, v := range req.Namespace.Labels {
			if err := store.SetLabel(ctx, req.Namespace.Name, k, v); err != nil {
				return err
			}
		}

		resp.Namespace = req.Namespace
		return nil
	})
}

func (s *Service) Update(ctx context.Context, req *api.UpdateNamespaceRequest) (*api.UpdateNamespaceResponse, error) {
	var resp api.UpdateNamespaceResponse
	return &resp, s.withStoreUpdate(ctx, func(ctx context.Context, store namespaces.Store) error {

		if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
			for _, path := range req.UpdateMask.Paths {
				switch {
				case strings.HasPrefix(path, "labels."):
					key := strings.TrimPrefix(path, "labels.")
					if err := store.SetLabel(ctx, req.Namespace.Name, key, req.Namespace.Labels[key]); err != nil {
						return err
					}
				default:
					return grpc.Errorf(codes.InvalidArgument, "cannot update %q field", path)
				}
			}
		} else {
			// clear out the existing labels and then set them to the incoming request.
			// get current set of labels
			labels, err := store.Labels(ctx, req.Namespace.Name)
			if err != nil {
				return mapGRPCError(err, req.Namespace.Name)
			}

			for k := range labels {
				if err := store.SetLabel(ctx, req.Namespace.Name, k, ""); err != nil {
					return err
				}
			}

			for k, v := range req.Namespace.Labels {
				if err := store.SetLabel(ctx, req.Namespace.Name, k, v); err != nil {
					return err
				}

			}
		}

		return nil
	})

}

func (s *Service) Delete(ctx context.Context, req *api.DeleteNamespaceRequest) (*empty.Empty, error) {
	return &empty.Empty{}, s.withStoreUpdate(ctx, func(ctx context.Context, store namespaces.Store) error {
		return mapGRPCError(store.Delete(ctx, req.Name), req.Name)
	})
}

func (s *Service) withStore(ctx context.Context, fn func(ctx context.Context, store namespaces.Store) error) func(tx *bolt.Tx) error {
	return func(tx *bolt.Tx) error { return fn(ctx, metadata.NewNamespaceStore(tx)) }
}

func (s *Service) withStoreView(ctx context.Context, fn func(ctx context.Context, store namespaces.Store) error) error {
	return s.db.View(s.withStore(ctx, fn))
}

func (s *Service) withStoreUpdate(ctx context.Context, fn func(ctx context.Context, store namespaces.Store) error) error {
	return s.db.Update(s.withStore(ctx, fn))
}
