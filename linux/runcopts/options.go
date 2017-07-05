package runcopts

import (
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/typeurl"
)

func init() {
	typeurl.Register(RuncOptions{}, "linux/runc/RuncOptions")
	typeurl.Register(CreateOptions{}, "linux/runc/CreateOptions")
	typeurl.Register(CheckpointOptions{}, "linux/runc/CheckpointOptions")
}

func WithExit(r *tasks.CheckpointTaskRequest) error {
	a, err := typeurl.MarshalAny(&CheckpointOptions{
		Exit: true,
	})
	r.Options = a
	return err
}
