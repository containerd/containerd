package opts

import (
	"context"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/linux/runcopts"
)

// WithContainerdShimCgroup returns function that sets the containerd
// shim cgroup path
func WithContainerdShimCgroup(path string) containerd.NewTaskOpts {
	return func(_ context.Context, _ *containerd.Client, r *containerd.TaskInfo) error {
		r.Options = &runcopts.CreateOptions{
			ShimCgroup: path,
		}
		return nil
	}
}

//TODO: Since Options is an interface different WithXXX will be needed to set different
// combinations of CreateOptions.
