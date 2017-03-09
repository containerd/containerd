package rootfs

import (
	"context"

	rootfsapi "github.com/docker/containerd/api/services/rootfs"
	containerd_v1_types "github.com/docker/containerd/api/types/descriptor"
	"github.com/docker/containerd/rootfs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func NewPreparerFromClient(client rootfsapi.RootFSClient) rootfs.Preparer {
	return remotePreparer{
		client: client,
	}
}

type remotePreparer struct {
	client rootfsapi.RootFSClient
}

func (rp remotePreparer) Prepare(ctx context.Context, layers []ocispec.Descriptor) (digest.Digest, error) {
	pr := rootfsapi.PrepareRequest{
		Layers: make([]*containerd_v1_types.Descriptor, len(layers)),
	}
	for i, l := range layers {
		pr.Layers[i] = &containerd_v1_types.Descriptor{
			MediaType: l.MediaType,
			Digest:    l.Digest,
			MediaSize: l.Size,
		}
	}
	resp, err := rp.client.Prepare(ctx, &pr)
	if err != nil {
		return "", nil
	}
	return resp.ChainID, nil
}
