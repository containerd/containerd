package containerd

import (
	"context"
	"encoding/json"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/rootfs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type Image interface {
	Name() string
	Target() ocispec.Descriptor

	Unpack(context.Context) error
}

var _ = (Image)(&image{})

type image struct {
	client *Client

	i images.Image
}

func (i *image) Name() string {
	return i.i.Name
}

func (i *image) Target() ocispec.Descriptor {
	return i.i.Target
}

func (i *image) Unpack(ctx context.Context) error {
	layers, err := i.getLayers(ctx)
	if err != nil {
		return err
	}
	if _, err := rootfs.ApplyLayers(ctx, layers, i.client.SnapshotService(), i.client.DiffService()); err != nil {
		return err
	}
	return nil
}

func (i *image) getLayers(ctx context.Context) ([]rootfs.Layer, error) {
	cs := i.client.ContentStore()
	p, err := content.ReadBlob(ctx, cs, i.i.Target.Digest)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read manifest blob")
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal manifest")
	}
	diffIDs, err := i.i.RootFS(ctx, cs)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve rootfs")
	}
	if len(diffIDs) != len(manifest.Layers) {
		return nil, errors.Errorf("mismatched image rootfs and manifest layers")
	}
	layers := make([]rootfs.Layer, len(diffIDs))
	for i := range diffIDs {
		layers[i].Diff = ocispec.Descriptor{
			// TODO: derive media type from compressed type
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    diffIDs[i],
		}
		layers[i].Blob = manifest.Layers[i]
	}
	return layers, nil
}
