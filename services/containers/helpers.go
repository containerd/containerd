package containers

import (
	api "github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/containers"
	"github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func containersToProto(containers []containers.Container) []api.Container {
	var containerspb []api.Container

	for _, image := range containers {
		containerspb = append(containerspb, containerToProto(&image))
	}

	return containerspb
}

func containerToProto(container *containers.Container) api.Container {
	return api.Container{
		ID:      container.ID,
		Labels:  container.Labels,
		Image:   container.Image,
		Runtime: container.Runtime,
		Spec: &types.Any{
			TypeUrl: specs.Version,
			Value:   container.Spec,
		},
		RootFS: container.RootFS,
	}
}

func containerFromProto(containerpb *api.Container) containers.Container {
	return containers.Container{
		ID:      containerpb.ID,
		Labels:  containerpb.Labels,
		Image:   containerpb.Image,
		Runtime: containerpb.Runtime,
		Spec:    containerpb.Spec.Value,
		RootFS:  containerpb.RootFS,
	}
}

func mapGRPCError(err error, id string) error {
	switch {
	case containers.IsNotFound(err):
		return grpc.Errorf(codes.NotFound, "container %v not found", id)
	case containers.IsExists(err):
		return grpc.Errorf(codes.AlreadyExists, "container %v already exists", id)
	}

	return err
}
