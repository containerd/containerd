package oci

import (
	"context"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/snapshot"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Client interface used by SpecOpt
type Client interface {
	SnapshotService(snapshotterName string) snapshot.Snapshotter
	ContentStore() content.Store
}

// Image interface used by some SpecOpt to query image configuration
type Image interface {
	// Config descriptor for the image.
	Config(ctx context.Context) (ocispec.Descriptor, error)
}
