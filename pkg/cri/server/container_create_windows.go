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
	"strconv"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/snapshots"
)

// No extra spec options needed for windows.
func (c *criService) containerSpecOpts(config *runtime.ContainerConfig, imageConfig *imagespec.ImageConfig) ([]oci.SpecOpts, error) {
	return nil, nil
}

// snapshotterOpts returns any Windows specific snapshotter options for the r/w layer
func snapshotterOpts(snapshotterName string, config *runtime.ContainerConfig) ([]snapshots.Opt, error) {
	var opts []snapshots.Opt

	switch snapshotterName {
	case "windows", "cimfs":
		rootfsSize := config.GetWindows().GetResources().GetRootfsSizeInBytes()
		if rootfsSize != 0 {
			labels := map[string]string{
				"containerd.io/snapshot/windows/rootfs.sizebytes": strconv.FormatInt(rootfsSize, 10),
			}
			opts = append(opts, snapshots.WithLabels(labels))
		}
	}

	return opts, nil
}
