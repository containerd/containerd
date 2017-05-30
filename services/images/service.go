package images

import (
	"github.com/boltdb/bolt"
	imagesapi "github.com/containerd/containerd/api/services/images"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register("images-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return NewService(ic.Meta), nil
		},
	})
}

type Service struct {
	db *bolt.DB
}

func NewService(db *bolt.DB) imagesapi.ImagesServer {
	return &Service{db: db}
}

func (s *Service) Register(server *grpc.Server) error {
	imagesapi.RegisterImagesServer(server, s)
	return nil
}

func (s *Service) Get(ctx context.Context, req *imagesapi.GetRequest) (*imagesapi.GetResponse, error) {
	var resp imagesapi.GetResponse

	return &resp, s.withStoreView(ctx, func(ctx context.Context, store images.Store) error {
		image, err := store.Get(ctx, req.Name)
		if err != nil {
			return mapGRPCError(err, req.Name)
		}
		imagepb := imageToProto(&image)
		resp.Image = &imagepb
		return nil
	})
}

func (s *Service) Put(ctx context.Context, req *imagesapi.PutRequest) (*empty.Empty, error) {
	return &empty.Empty{}, s.withStoreUpdate(ctx, func(ctx context.Context, store images.Store) error {
		return mapGRPCError(store.Put(ctx, req.Image.Name, descFromProto(&req.Image.Target)), req.Image.Name)
	})
}

func (s *Service) List(ctx context.Context, _ *imagesapi.ListRequest) (*imagesapi.ListResponse, error) {
	var resp imagesapi.ListResponse

	return &resp, s.withStoreView(ctx, func(ctx context.Context, store images.Store) error {
		images, err := store.List(ctx)
		if err != nil {
			return mapGRPCError(err, "")
		}

		resp.Images = imagesToProto(images)
		return nil
	})
}

func (s *Service) Delete(ctx context.Context, req *imagesapi.DeleteRequest) (*empty.Empty, error) {
	return &empty.Empty{}, s.withStoreUpdate(ctx, func(ctx context.Context, store images.Store) error {
		return mapGRPCError(store.Delete(ctx, req.Name), req.Name)
	})
}

func (s *Service) withStore(ctx context.Context, fn func(ctx context.Context, store images.Store) error) func(tx *bolt.Tx) error {
	return func(tx *bolt.Tx) error { return fn(ctx, metadata.NewImageStore(tx)) }
}

func (s *Service) withStoreView(ctx context.Context, fn func(ctx context.Context, store images.Store) error) error {
	return s.db.View(s.withStore(ctx, fn))
}

func (s *Service) withStoreUpdate(ctx context.Context, fn func(ctx context.Context, store images.Store) error) error {
	return s.db.Update(s.withStore(ctx, fn))
}
