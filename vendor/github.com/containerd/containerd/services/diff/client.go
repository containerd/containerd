package diff

import (
	diffapi "github.com/containerd/containerd/api/services/diff"
	"github.com/containerd/containerd/api/types/descriptor"
	mounttypes "github.com/containerd/containerd/api/types/mount"
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

func toDescriptor(d *descriptor.Descriptor) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: d.MediaType,
		Digest:    d.Digest,
		Size:      d.Size_,
	}
}

func fromDescriptor(d ocispec.Descriptor) *descriptor.Descriptor {
	return &descriptor.Descriptor{
		MediaType: d.MediaType,
		Digest:    d.Digest,
		Size_:     d.Size,
	}
}

func fromMounts(mounts []mount.Mount) []*mounttypes.Mount {
	apiMounts := make([]*mounttypes.Mount, len(mounts))
	for i, m := range mounts {
		apiMounts[i] = &mounttypes.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		}
	}
	return apiMounts
}
