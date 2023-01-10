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

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/oci"
	customopts "github.com/containerd/containerd/pkg/cri/opts"
	"github.com/containerd/containerd/snapshots"
)

// No container mounts for windows.
func (c *criService) containerMounts(sandboxID string, config *runtime.ContainerConfig) []*runtime.Mount {
	return nil
}

func (c *criService) platformSpec(
	id string,
	sandboxID string,
	config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig,
	extraMounts []*runtime.Mount,
) ([]oci.SpecOpts, error) {
	specOpts := []oci.SpecOpts{}

	specOpts = append(specOpts,
		customopts.WithWindowsMounts(c.os, config, extraMounts),
		customopts.WithDevices(config),
	)

	// Start with the image config user and override below if RunAsUsername is not "".
	username := imageConfig.User

	windowsConfig := config.GetWindows()
	if windowsConfig != nil {
		specOpts = append(specOpts, customopts.WithWindowsResources(windowsConfig.GetResources()))
		securityCtx := windowsConfig.GetSecurityContext()
		if securityCtx != nil {
			runAsUser := securityCtx.GetRunAsUsername()
			if runAsUser != "" {
				username = runAsUser
			}
			cs := securityCtx.GetCredentialSpec()
			if cs != "" {
				specOpts = append(specOpts, customopts.WithWindowsCredentialSpec(cs))
			}
		}
	}

	// There really isn't a good Windows way to verify that the username is available in the
	// image as early as here like there is for Linux. Later on in the stack hcsshim
	// will handle the behavior of erroring out if the user isn't available in the image
	// when trying to run the init process.
	specOpts = append(specOpts, oci.WithUser(username))

	return specOpts, nil
}

// No extra spec options needed for windows.
func (c *criService) containerSpecOpts(config *runtime.ContainerConfig, imageConfig *imagespec.ImageConfig) ([]oci.SpecOpts, error) {
	return nil, nil
}

// snapshotterOpts returns any Windows specific snapshotter options for the r/w layer
func snapshotterOpts(snapshotterName string, config *runtime.ContainerConfig) []snapshots.Opt {
	var opts []snapshots.Opt

	switch snapshotterName {
	case "windows":
		rootfsSize := config.GetWindows().GetResources().GetRootfsSizeInBytes()
		if rootfsSize != 0 {
			sizeStr := fmt.Sprintf("%d", rootfsSize)
			labels := map[string]string{
				"containerd.io/snapshot/windows/rootfs.sizebytes": sizeStr,
			}
			opts = append(opts, snapshots.WithLabels(labels))
		}
	}

	return opts
}
