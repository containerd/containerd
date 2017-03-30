package images

import (
	imagesapi "github.com/docker/containerd/api/services/images"
	"github.com/docker/containerd/images"
	"github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
)

type Service struct {
	store images.Store
}

func NewService(store images.Store) imagesapi.ImagesServer {
	return &Service{store: store}
}

func (s *Service) Get(context.Context, *imagesapi.GetRequest) (*imagesapi.GetResponse, error) {
	panic("not implemented")
}

func (s *Service) Register(context.Context, *imagesapi.RegisterRequest) (*imagesapi.RegisterRequest, error) {
	panic("not implemented")
}

func (s *Service) List(context.Context, *imagesapi.ListRequest) (*imagesapi.ListResponse, error) {
	panic("not implemented")
}

func (s *Service) Delete(context.Context, *imagesapi.DeleteRequest) (*empty.Empty, error) {
	panic("not implemented")
}
