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
	"fmt"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/internal/userns"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// updateContainerIOOwner updates I/O files' owner to align with initial processe's UID/GID.
func updateContainerIOOwner(ctx context.Context, cntr containerd.Container, config *runtime.ContainerConfig) ([]containerd.NewTaskOpts, error) {
	if config.GetLinux() == nil {
		return nil, nil
	}

	// FIXME(fuweid): Ideally, the pipe owner should be aligned with process owner.
	// No matter what user namespace container uses, it should work well. However,
	// it breaks the sig-node conformance case - [when querying /stats/summary should report resource usage through the stats api].
	// In order to keep compatible, the change should apply to user namespace only.
	if config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetUsernsOptions() == nil {
		return nil, nil
	}

	spec, err := cntr.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get spec: %w", err)
	}

	if spec.Linux == nil || spec.Process == nil {
		return nil, fmt.Errorf("invalid linux platform oci runtime spec")
	}

	hostID, err := userns.IDMap{
		UidMap: spec.Linux.UIDMappings,
		GidMap: spec.Linux.GIDMappings,
	}.ToHost(userns.User{
		Uid: spec.Process.User.UID,
		Gid: spec.Process.User.GID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to do idmap to get host ID: %w", err)
	}

	return []containerd.NewTaskOpts{
		containerd.WithUIDOwner(hostID.Uid),
		containerd.WithGIDOwner(hostID.Gid),
	}, nil
}
