package images

import (
	"github.com/boltdb/bolt"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	"github.com/containerd/containerd/api/types/event"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "images",
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

func NewService(db *bolt.DB, evts events.Poster) imagesapi.ImagesServer {
	return &Service{
		db:      db,
		emitter: evts,
	}
}

func (s *Service) Register(server *grpc.Server) error {
	imagesapi.RegisterImagesServer(server, s)
	return nil
}

func (s *Service) Get(ctx context.Context, req *imagesapi.GetImageRequest) (*imagesapi.GetImageResponse, error) {
	var resp imagesapi.GetImageResponse

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

func (s *Service) Update(ctx context.Context, req *imagesapi.UpdateImageRequest) (*imagesapi.UpdateImageResponse, error) {
	if err := s.withStoreUpdate(ctx, func(ctx context.Context, store images.Store) error {
		return mapGRPCError(store.Update(ctx, req.Image.Name, descFromProto(&req.Image.Target)), req.Image.Name)
	}); err != nil {
		return nil, err
	}

	if err := s.emit(ctx, "/images/update", event.ImageUpdate{
		Name:   req.Image.Name,
		Labels: req.Image.Labels,
	}); err != nil {
		return nil, err
	}

	// TODO: get image back out to return
	return &imagesapi.UpdateImageResponse{
	//Image: nil,
	}, nil
}

func (s *Service) List(ctx context.Context, _ *imagesapi.ListImagesRequest) (*imagesapi.ListImagesResponse, error) {
	var resp imagesapi.ListImagesResponse

	return &resp, s.withStoreView(ctx, func(ctx context.Context, store images.Store) error {
		images, err := store.List(ctx)
		if err != nil {
			return mapGRPCError(err, "")
		}

		resp.Images = imagesToProto(images)
		return nil
	})
}

func (s *Service) Delete(ctx context.Context, req *imagesapi.DeleteImageRequest) (*empty.Empty, error) {
	if err := s.withStoreUpdate(ctx, func(ctx context.Context, store images.Store) error {
		return mapGRPCError(store.Delete(ctx, req.Name), req.Name)
	}); err != nil {
		return nil, err
	}

	if err := s.emit(ctx, "/images/delete", event.ImageDelete{
		Name: req.Name,
	}); err != nil {
		return nil, err
	}

	return &empty.Empty{}, nil
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

func (s *Service) emit(ctx context.Context, topic string, evt interface{}) error {
	emitterCtx := events.WithTopic(ctx, topic)
	if err := s.emitter.Post(emitterCtx, evt); err != nil {
		return err
	}

	return nil
}
