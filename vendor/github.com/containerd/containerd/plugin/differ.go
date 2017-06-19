package plugin

import (
	"github.com/containerd/containerd/mount"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/context"
)

type Differ interface {
	Apply(ctx context.Context, desc ocispec.Descriptor, mount []mount.Mount) (ocispec.Descriptor, error)
	DiffMounts(ctx context.Context, lower, upper []mount.Mount, media, ref string) (ocispec.Descriptor, error)
}
