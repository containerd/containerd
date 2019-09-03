// +build windows

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
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/oci"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// TODO(windows): Add windows support.
// TODO(windows): Configure windows sandbox shares
func (c *criService) sandboxContainerSpec(id string, config *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig, nsPath string, runtimePodAnnotations []string) (*runtimespec.Spec, error) {
	return nil, errdefs.ErrNotImplemented
}

// No sandbox container spec options for windows yet.
func (c *criService) sandboxContainerSpecOpts(config *runtime.PodSandboxConfig, imageConfig *imagespec.ImageConfig) ([]oci.SpecOpts, error) {
	return nil, nil
}

// No sandbox files needed for windows.
func (c *criService) setupSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	return nil
}

// No sandbox files needed for windows.
func (c *criService) cleanupSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	return nil
}

// No task options needed for windows.
func (c *criService) taskOpts(runtimeType string) []containerd.NewTaskOpts {
	return nil
}
