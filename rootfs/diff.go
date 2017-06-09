package rootfs

import (
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/context"
)

type MountDiffer interface {
	DiffMounts(ctx context.Context, lower, upper []mount.Mount, media, ref string) (ocispec.Descriptor, error)
}

type DiffOptions struct {
	MountDiffer
	content.Store
	snapshot.Snapshotter
}

func Diff(ctx context.Context, snapshotID, contentRef string, sn snapshot.Snapshotter, md MountDiffer) (ocispec.Descriptor, error) {
	info, err := sn.Stat(ctx, snapshotID)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	lowerKey := fmt.Sprintf("%s-parent-view", info.Parent)
	lower, err := sn.View(ctx, lowerKey, info.Parent)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer sn.Remove(ctx, lowerKey)

	var upper []mount.Mount
	if info.Kind == snapshot.KindActive {
		upper, err = sn.Mounts(ctx, snapshotID)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
	} else {
		upperKey := fmt.Sprintf("%s-view", snapshotID)
		upper, err = sn.View(ctx, upperKey, snapshotID)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		defer sn.Remove(ctx, lowerKey)
	}

	return md.DiffMounts(ctx, lower, upper, ocispec.MediaTypeImageLayer, contentRef)
}
