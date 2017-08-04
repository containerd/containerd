package containerd

import (
	"context"

	"github.com/containerd/containerd/linux/runcopts"
	"github.com/containerd/containerd/mount"
)

// NewTaskOpts allows the caller to set options on a new task
type NewTaskOpts func(context.Context, *Client, *TaskInfo) error

// WithRootFS allows a task to be created without a snapshot being allocated to its container
func WithRootFS(mounts []mount.Mount) NewTaskOpts {
	return func(ctx context.Context, c *Client, ti *TaskInfo) error {
		ti.RootFS = mounts
		return nil
	}
}

// WithExit causes the task to exit after a successful checkpoint
func WithExit(r *CheckpointTaskInfo) error {
	r.Options = &runcopts.CheckpointOptions{
		Exit: true,
	}
	return nil
}
