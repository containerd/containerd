package diff

import (
	diffapi "github.com/containerd/containerd/api/services/diff/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/rootfs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/context"
)

type DiffService interface {
	rootfs.Applier
	rootfs.MountDiffer
}

// NewApplierFromClient returns a new Applier which communicates
// over a GRPC connection.
func NewDiffServiceFromClient(client diffapi.DiffClient) DiffService {
	return &remote{
		client: client,
	}
}

type remote struct {
	client diffapi.DiffClient
}

func (r *remote) Apply(ctx context.Context, diff ocispec.Descriptor, mounts []mount.Mount) (ocispec.Descriptor, error) {
	req := &diffapi.ApplyRequest{
		Diff:   fromDescriptor(diff),
		Mounts: fromMounts(mounts),
	}
	resp, err := r.client.Apply(ctx, req)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return toDescriptor(resp.Applied), nil
}

func (r *remote) DiffMounts(ctx context.Context, a, b []mount.Mount, media, ref string) (ocispec.Descriptor, error) {
	req := &diffapi.DiffRequest{
		Left:      fromMounts(a),
		Right:     fromMounts(b),
		MediaType: media,
		Ref:       ref,
	}
	resp, err := r.client.Diff(ctx, req)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return toDescriptor(resp.Diff), nil
}

func toDescriptor(d *types.Descriptor) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: d.MediaType,
		Digest:    d.Digest,
		Size:      d.Size_,
	}
}

func fromDescriptor(d ocispec.Descriptor) *types.Descriptor {
	return &types.Descriptor{
		MediaType: d.MediaType,
		Digest:    d.Digest,
		Size_:     d.Size,
	}
}

func fromMounts(mounts []mount.Mount) []*types.Mount {
	apiMounts := make([]*types.Mount, len(mounts))
	for i, m := range mounts {
		apiMounts[i] = &types.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		}
	}
	return apiMounts
}
