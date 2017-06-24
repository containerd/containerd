package containers

import (
	api "github.com/containerd/containerd/api/services/containers/v1"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/identifiers"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
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
		ID:     container.ID,
		Labels: container.Labels,
		Image:  container.Image,
		Runtime: &api.Container_Runtime{
			Name:    container.Runtime.Name,
			Options: container.Runtime.Options,
		},
		Spec: &types.Any{
			TypeUrl: specs.Version,
			Value:   container.Spec,
		},
		RootFS: container.RootFS,
	}
}

func containerFromProto(containerpb *api.Container) containers.Container {
	return containers.Container{
		ID:     containerpb.ID,
		Labels: containerpb.Labels,
		Image:  containerpb.Image,
		Runtime: containers.RuntimeInfo{
			Name:    containerpb.Runtime.Name,
			Options: containerpb.Runtime.Options,
		},
		Spec:   containerpb.Spec.Value,
		RootFS: containerpb.RootFS,
	}
}

func mapGRPCError(err error, id string) error {
	switch {
	case metadata.IsNotFound(err):
		return grpc.Errorf(codes.NotFound, "container %v not found", id)
	case metadata.IsExists(err):
		return grpc.Errorf(codes.AlreadyExists, "container %v already exists", id)
	case namespaces.IsNamespaceRequired(err):
		return grpc.Errorf(codes.InvalidArgument, "namespace required, please set %q header", namespaces.GRPCHeader)
	case identifiers.IsInvalid(err):
		return grpc.Errorf(codes.InvalidArgument, err.Error())
	}

	return err
}
