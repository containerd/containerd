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

package podsandbox

import (
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/cri/annotations"
	customopts "github.com/containerd/containerd/pkg/cri/opts"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *Controller) sandboxContainerSpec(id string, config *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig, nsPath string, runtimePodAnnotations []string) (_ *runtimespec.Spec, retErr error) {
	specOpts := []oci.SpecOpts{
		customopts.WithAnnotation(annotations.ContainerType, annotations.ContainerTypeSandbox),
		customopts.WithAnnotation(annotations.SandboxID, id),
		customopts.WithAnnotation(annotations.SandboxNamespace, config.GetMetadata().GetNamespace()),
		customopts.WithAnnotation(annotations.SandboxUID, config.GetMetadata().GetUid()),
		customopts.WithAnnotation(annotations.SandboxName, config.GetMetadata().GetName()),
		customopts.WithAnnotation(annotations.SandboxLogDir, config.GetLogDirectory()),
	}
	return c.runtimeSpec(id, "", specOpts...)
}

// sandboxContainerSpecOpts generates OCI spec options for
// the sandbox container.
func (c *Controller) sandboxContainerSpecOpts(config *runtime.PodSandboxConfig, imageConfig *imagespec.ImageConfig) ([]oci.SpecOpts, error) {
	return []oci.SpecOpts{}, nil
}

// setupSandboxFiles sets up necessary sandbox files including /dev/shm, /etc/hosts,
// /etc/resolv.conf and /etc/hostname.
func (c *Controller) setupSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	return nil
}

// cleanupSandboxFiles unmount some sandbox files, we rely on the removal of sandbox root directory to
// remove these files. Unmount should *NOT* return error if the mount point is already unmounted.
func (c *Controller) cleanupSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	return nil
}

// taskOpts generates task options for a (sandbox) container.
func (c *Controller) taskOpts(runtimeType string) []containerd.NewTaskOpts {
	return []containerd.NewTaskOpts{}
}
