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
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/plugin"
)

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
