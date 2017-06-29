package runcopts

import (
	"path/filepath"

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/gogo/protobuf/proto"
	protobuf "github.com/gogo/protobuf/types"
)

const URIBase = "types.containerd.io/linux/runc"

func WithExit(r *tasks.CheckpointTaskRequest) error {
	a, err := marshal(&CheckpointOptions{
		Exit: true,
	}, "CheckpointOptions")
	if err != nil {
		return err
	}
	r.Options = a
	return nil
}

func marshal(m proto.Message, name string) (*protobuf.Any, error) {
	data, err := proto.Marshal(m)
	if err != nil {
		return nil, err
	}
	return &protobuf.Any{
		TypeUrl: filepath.Join(URIBase, name),
		Value:   data,
	}, nil
}
