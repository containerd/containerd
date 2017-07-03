package containers

import (
	api "github.com/containerd/containerd/api/services/containers/v1"
	"github.com/containerd/containerd/containers"
	"github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	//TypeURLPrefix adds prefix to make OCI spec version as per protobuf's TypeUrl
	TypeURLPrefix = "types.containerd.io/opencontainers/runtime-spec/"
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
			TypeUrl: TypeURLPrefix + specs.Version,
			Value:   container.Spec,
		},
		RootFS: container.RootFS,
	}
}

func containerFromProto(containerpb *api.Container) containers.Container {
	var runtime containers.RuntimeInfo
	if containerpb.Runtime != nil {
		runtime = containers.RuntimeInfo{
			Name:    containerpb.Runtime.Name,
			Options: containerpb.Runtime.Options,
		}
	}

	var spec []byte
	if containerpb.Spec != nil {
		spec = containerpb.Spec.Value
	}

	return containers.Container{
		ID:      containerpb.ID,
		Labels:  containerpb.Labels,
		Image:   containerpb.Image,
		Runtime: runtime,
		Spec:    spec,
		RootFS:  containerpb.RootFS,
	}
}
