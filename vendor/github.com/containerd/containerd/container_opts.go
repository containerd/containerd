package containerd

import (
	"context"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/opencontainers/image-spec/identity"
	"github.com/pkg/errors"
)

// NewContainerOpts allows the caller to set additional options when creating a container
type NewContainerOpts func(ctx context.Context, client *Client, c *containers.Container) error

// WithRuntime allows a user to specify the runtime name and additional options that should
// be used to create tasks for the container
func WithRuntime(name string) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		c.Runtime = containers.RuntimeInfo{
			Name: name,
		}
		return nil
	}
}

// WithImage sets the provided image as the base for the container
func WithImage(i Image) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		c.Image = i.Name()
		return nil
	}
}

// WithContainerLabels adds the provided labels to the container
func WithContainerLabels(labels map[string]string) NewContainerOpts {
	return func(_ context.Context, _ *Client, c *containers.Container) error {
		c.Labels = labels
		return nil
	}
}

// WithSnapshotter sets the provided snapshotter for use by the container
//
// This option must appear before other snapshotter options to have an effect.
func WithSnapshotter(name string) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		c.Snapshotter = name
		return nil
	}
}

// WithSnapshot uses an existing root filesystem for the container
func WithSnapshot(id string) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		setSnapshotterIfEmpty(c)
		// check that the snapshot exists, if not, fail on creation
		if _, err := client.SnapshotService(c.Snapshotter).Mounts(ctx, id); err != nil {
			return err
		}
		c.RootFS = id
		return nil
	}
}

// WithNewSnapshot allocates a new snapshot to be used by the container as the
// root filesystem in read-write mode
func WithNewSnapshot(id string, i Image) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		diffIDs, err := i.(*image).i.RootFS(ctx, client.ContentStore())
		if err != nil {
			return err
		}
		setSnapshotterIfEmpty(c)
		if _, err := client.SnapshotService(c.Snapshotter).Prepare(ctx, id, identity.ChainID(diffIDs).String()); err != nil {
			return err
		}
		c.RootFS = id
		c.Image = i.Name()
		return nil
	}
}

// WithSnapshotCleanup deletes the rootfs allocated for the container
func WithSnapshotCleanup(ctx context.Context, client *Client, c containers.Container) error {
	if c.RootFS != "" {
		if c.Snapshotter == "" {
			return errors.Wrapf(errdefs.ErrInvalidArgument, "container.Snapshotter must be set to cleanup rootfs")
		}
		return client.SnapshotService(c.Snapshotter).Remove(ctx, c.RootFS)
	}
	return nil
}

// WithNewSnapshotView allocates a new snapshot to be used by the container as the
// root filesystem in read-only mode
func WithNewSnapshotView(id string, i Image) NewContainerOpts {
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		diffIDs, err := i.(*image).i.RootFS(ctx, client.ContentStore())
		if err != nil {
			return err
		}
		setSnapshotterIfEmpty(c)
		if _, err := client.SnapshotService(c.Snapshotter).View(ctx, id, identity.ChainID(diffIDs).String()); err != nil {
			return err
		}
		c.RootFS = id
		c.Image = i.Name()
		return nil
	}
}

func setSnapshotterIfEmpty(c *containers.Container) {
	if c.Snapshotter == "" {
		c.Snapshotter = DefaultSnapshotter
	}
}
