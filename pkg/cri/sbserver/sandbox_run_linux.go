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

package sbserver

import (
	"fmt"
	"os"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/plugin"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	osinterface "github.com/containerd/containerd/pkg/os"
)

// cleanupSandboxFiles unmount some sandbox files, we rely on the removal of sandbox root directory to
// remove these files. Unmount should *NOT* return error if the mount point is already unmounted.
func (c *criService) cleanupSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	if config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetIpc() != runtime.NamespaceMode_NODE {
		path, err := c.os.FollowSymlinkInScope(c.getSandboxDevShm(id), "/")
		if err != nil {
			return fmt.Errorf("failed to follow symlink: %w", err)
		}
		if err := c.os.(osinterface.UNIX).Unmount(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to unmount %q: %w", path, err)
		}
	}
	return nil
}

// taskOpts generates task options for a (sandbox) container.
func (c *criService) taskOpts(runtimeType string) []containerd.NewTaskOpts {
	// TODO(random-liu): Remove this after shim v1 is deprecated.
	var taskOpts []containerd.NewTaskOpts

	// c.config.NoPivot is only supported for RuntimeLinuxV1 = "io.containerd.runtime.v1.linux" legacy linux runtime
	// and is not supported for RuntimeRuncV1 = "io.containerd.runc.v1" or  RuntimeRuncV2 = "io.containerd.runc.v2"
	// for RuncV1/2 no pivot is set under the containerd.runtimes.runc.options config see
	// https://github.com/containerd/containerd/blob/v1.3.2/runtime/v2/runc/options/oci.pb.go#L26
	if c.config.NoPivot && runtimeType == plugin.RuntimeLinuxV1 {
		taskOpts = append(taskOpts, containerd.WithNoPivotRoot)
	}

	return taskOpts
}
