//go:build !windows && !linux

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
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/snapshots"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// containerMounts sets up necessary container system file mounts
// including /dev/shm, /etc/hosts and /etc/resolv.conf.
func (c *criService) containerMounts(sandboxID string, config *runtime.ContainerConfig) []*runtime.Mount {
	return []*runtime.Mount{}
}

func (c *criService) platformSpec(
	id string,
	sandboxID string,
	config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig,
	extraMounts []*runtime.Mount,
) ([]oci.SpecOpts, error) {
	return []oci.SpecOpts{}, nil
}

func (c *criService) containerSpecOpts(config *runtime.ContainerConfig, imageConfig *imagespec.ImageConfig) ([]oci.SpecOpts, error) {
	return []oci.SpecOpts{}, nil
}

// snapshotterOpts returns snapshotter options for the rootfs snapshot
func snapshotterOpts(snapshotterName string, config *runtime.ContainerConfig) []snapshots.Opt {
	return []snapshots.Opt{}
}
