/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package server

import (
	"context"
	"errors"
	"fmt"

	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func toCRIContainerUser(ctx context.Context, container containerstore.Container) (*runtime.ContainerUser, error) {
	if container.Container == nil {
		return nil, errors.New("container must not be nil")
	}

	runtimeSpec, err := container.Container.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container runtime spec: %w", err)
	}

	if runtimeSpec.Process == nil {
		return &runtime.ContainerUser{}, nil
	}

	user := runtimeSpec.Process.User
	var supplementalGroups []int64
	for _, gid := range user.AdditionalGids {
		supplementalGroups = append(supplementalGroups, int64(gid))
	}
	return &runtime.ContainerUser{
		Linux: &runtime.LinuxContainerUser{
			Uid:                int64(user.UID),
			Gid:                int64(user.GID),
			SupplementalGroups: supplementalGroups,
		},
	}, nil
}
