package images

import (
	"context"

	imagesapi "github.com/containerd/containerd/api/services/images"
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

func (s *remoteStore) Put(ctx context.Context, name string, desc ocispec.Descriptor) error {
	// TODO(stevvooe): Consider that the remote may want to augment and return
	// a modified image.
	_, err := s.client.Put(ctx, &imagesapi.PutRequest{
		Image: imagesapi.Image{
			Name:   name,
			Target: descToProto(&desc),
		},
	})

	return rewriteGRPCError(err)
}

func (s *remoteStore) Get(ctx context.Context, name string) (images.Image, error) {
	resp, err := s.client.Get(ctx, &imagesapi.GetRequest{
		Name: name,
	})
	if err != nil {
		return images.Image{}, rewriteGRPCError(err)
	}

	return imageFromProto(resp.Image), nil
}

func (s *remoteStore) List(ctx context.Context) ([]images.Image, error) {
	resp, err := s.client.List(ctx, &imagesapi.ListRequest{})
	if err != nil {
		return nil, rewriteGRPCError(err)
	}

	return imagesFromProto(resp.Images), nil
}

func (s *remoteStore) Delete(ctx context.Context, name string) error {
	_, err := s.client.Delete(ctx, &imagesapi.DeleteRequest{
		Name: name,
	})

	return rewriteGRPCError(err)
}
