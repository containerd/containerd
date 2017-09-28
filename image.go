package containerd

import (
	"context"
	"strings"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/rootfs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Image describes an image used by containers
type Image interface {
	// Name of the image
	Name() string
	// Labels of the image
	Labels() map[string]string
	// Target descriptor for the image content
	Target() ocispec.Descriptor
	// Unpack unpacks the image's content into a snapshot
	Unpack(context.Context, string) error
	// RootFS returns the unpacked diffids that make up images rootfs.
	RootFS(ctx context.Context) ([]digest.Digest, error)
	// Size returns the total size of the image's packed resources.
	Size(ctx context.Context) (int64, error)
	// Config descriptor for the image.
	Config(ctx context.Context) (ocispec.Descriptor, error)
}

var _ = (Image)(&image{})

type image struct {
	client *Client

	i images.Image
}

func (i *image) Name() string {
	return i.i.Name
}

func (i *image) Labels() map[string]string {
	return i.i.Labels
}

func (i *image) Target() ocispec.Descriptor {
	return i.i.Target
}

func (i *image) RootFS(ctx context.Context) ([]digest.Digest, error) {
	provider := i.client.ContentStore()
	return i.i.RootFS(ctx, provider, platforms.Default())
}

func (i *image) Size(ctx context.Context) (int64, error) {
	provider := i.client.ContentStore()
	return i.i.Size(ctx, provider, platforms.Default())
}

func (i *image) Config(ctx context.Context) (ocispec.Descriptor, error) {
	provider := i.client.ContentStore()
	return i.i.Config(ctx, provider, platforms.Default())
}

func (i *image) Unpack(ctx context.Context, snapshotterName string) error {
	layers, err := i.getLayers(ctx, platforms.Default())
	if err != nil {
		return err
	}

	sn := i.client.SnapshotService(snapshotterName)
	a := i.client.DiffService()
	cs := i.client.ContentStore()

	snLabels := make(map[string]string)
	if iLabels := i.Labels(); iLabels != nil {
		// The snapshotter plugin MAY use these `containerd.io/image.remote.*` labels to lazy-pull blobs from the registry.
		// See *Client.Pull() for the labels actually set on pull.
		prefix := "containerd.io/image.remote."
		for k, v := range iLabels {
			if strings.HasPrefix(k, prefix) {
				snLabels[k] = v
			}
		}
	}

	var chain []digest.Digest
	for _, layer := range layers {
		unpacked, err := rootfs.ApplyLayer(ctx, layer, chain, sn, snLabels, a)
		if err != nil {
			// TODO: possibly wait and retry if extraction of same chain id was in progress
			return err
		}
		if unpacked {
			info, err := cs.Info(ctx, layer.Blob.Digest)
			if err != nil {
				return err
			}
			if info.Labels == nil {
				info.Labels = map[string]string{}
			}
			if info.Labels["containerd.io/uncompressed"] != layer.Diff.Digest.String() {
				info.Labels["containerd.io/uncompressed"] = layer.Diff.Digest.String()
				if _, err := cs.Update(ctx, info, "labels.containerd.io/uncompressed"); err != nil {
					return err
				}
			}
		}

		chain = append(chain, layer.Diff.Digest)
	}

	return nil
}

func (i *image) getLayers(ctx context.Context, platform string) ([]rootfs.Layer, error) {
	cs := i.client.ContentStore()

	manifest, err := images.Manifest(ctx, cs, i.i.Target, platform)
	if err != nil {
		return nil, errors.Wrap(err, "")
	}

	diffIDs, err := i.i.RootFS(ctx, cs, platform)
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
