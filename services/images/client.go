package images

import (
	"context"

	imagesapi "github.com/containerd/containerd/api/services/images"
	"github.com/containerd/containerd/images"
)

type remoteStore struct {
	client imagesapi.ImagesClient
}

func NewStoreFromClient(client imagesapi.ImagesClient) images.Store {
	return &remoteStore{
		client: client,
	}
}

func (s *remoteStore) Update(ctx context.Context, image images.Image) error {
	// TODO(stevvooe): Consider that the remote may want to augment and return
	// a modified image.
	_, err := s.client.Update(ctx, &imagesapi.UpdateImageRequest{
		Image: imagesapi.Image{
			Name:   image.Name,
			Labels: image.Labels,
			Target: descToProto(&image.Target),
		},
	})

	return rewriteGRPCError(err)
}

func (s *remoteStore) Get(ctx context.Context, name string) (images.Image, error) {
	resp, err := s.client.Get(ctx, &imagesapi.GetImageRequest{
		Name: name,
	})
	if err != nil {
		return images.Image{}, rewriteGRPCError(err)
	}

	return imageFromProto(resp.Image), nil
}

func (s *remoteStore) List(ctx context.Context) ([]images.Image, error) {
	resp, err := s.client.List(ctx, &imagesapi.ListImagesRequest{})
	if err != nil {
		return nil, rewriteGRPCError(err)
	}

	return imagesFromProto(resp.Images), nil
}

func (s *remoteStore) Delete(ctx context.Context, name string) error {
	_, err := s.client.Delete(ctx, &imagesapi.DeleteImageRequest{
		Name: name,
	})

	return rewriteGRPCError(err)
}
