package images

import (
	"context"

	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type remoteStore struct {
	client imagesapi.ImagesClient
}

func NewStoreFromClient(client imagesapi.ImagesClient) images.Store {
	return &remoteStore{
		client: client,
	}
}

func (s *remoteStore) Update(ctx context.Context, name string, desc ocispec.Descriptor) error {
	// TODO(stevvooe): Consider that the remote may want to augment and return
	// a modified image.
	_, err := s.client.Update(ctx, &imagesapi.UpdateImageRequest{
		Image: imagesapi.Image{
			Name:   name,
			Target: descToProto(&desc),
		},
	})

	return errdefs.FromGRPC(err)
}

func (s *remoteStore) Get(ctx context.Context, name string) (images.Image, error) {
	resp, err := s.client.Get(ctx, &imagesapi.GetImageRequest{
		Name: name,
	})
	if err != nil {
		return images.Image{}, errdefs.FromGRPC(err)
	}

	return imageFromProto(resp.Image), nil
}

func (s *remoteStore) List(ctx context.Context) ([]images.Image, error) {
	resp, err := s.client.List(ctx, &imagesapi.ListImagesRequest{})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}

	return imagesFromProto(resp.Images), nil
}

func (s *remoteStore) Delete(ctx context.Context, name string) error {
	_, err := s.client.Delete(ctx, &imagesapi.DeleteImageRequest{
		Name: name,
	})

	return errdefs.FromGRPC(err)
}
