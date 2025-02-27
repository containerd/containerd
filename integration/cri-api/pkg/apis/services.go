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

/*
Copyright 2016 The Kubernetes Authors.

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

// this file is from k8s.io/cri-api/pkg/apis only it points to v1 as the runtimeapi not v1alpha

package cri

import (
	"context"
	"time"

	"google.golang.org/grpc"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// RuntimeVersioner contains methods for runtime name, version and API version.
type RuntimeVersioner interface {
	// Version returns the runtime name, runtime version and runtime API version
	Version(apiVersion string, opts ...grpc.CallOption) (*runtimeapi.VersionResponse, error)
}

// ContainerManager contains methods to manipulate containers managed by a
// container runtime. The methods are thread-safe.
type ContainerManager interface {
	// CreateContainer creates a new container in specified PodSandbox.
	CreateContainer(podSandboxID string, config *runtimeapi.ContainerConfig, sandboxConfig *runtimeapi.PodSandboxConfig, opts ...grpc.CallOption) (string, error)
	// StartContainer starts the container.
	StartContainer(containerID string, opts ...grpc.CallOption) error
	// StopContainer stops a running container with a grace period (i.e., timeout).
	StopContainer(containerID string, timeout int64, opts ...grpc.CallOption) error
	// RemoveContainer removes the container.
	RemoveContainer(containerID string, opts ...grpc.CallOption) error
	// ListContainers lists all containers by filters.
	ListContainers(filter *runtimeapi.ContainerFilter, opts ...grpc.CallOption) ([]*runtimeapi.Container, error)
	// ContainerStatus returns the status of the container.
	ContainerStatus(containerID string, opts ...grpc.CallOption) (*runtimeapi.ContainerStatus, error)
	// UpdateContainerResources updates the cgroup resources for the container.
	UpdateContainerResources(containerID string, resources *runtimeapi.LinuxContainerResources, windowsResources *runtimeapi.WindowsContainerResources, opts ...grpc.CallOption) error
	// ExecSync executes a command in the container, and returns the stdout output.
	// If command exits with a non-zero exit code, an error is returned.
	ExecSync(containerID string, cmd []string, timeout time.Duration, opts ...grpc.CallOption) (stdout []byte, stderr []byte, err error)
	// Exec prepares a streaming endpoint to execute a command in the container, and returns the address.
	Exec(req *runtimeapi.ExecRequest, opts ...grpc.CallOption) (*runtimeapi.ExecResponse, error)
	// Attach prepares a streaming endpoint to attach to a running container, and returns the address.
	Attach(req *runtimeapi.AttachRequest, opts ...grpc.CallOption) (*runtimeapi.AttachResponse, error)
	// ReopenContainerLog asks runtime to reopen the stdout/stderr log file
	// for the container. If it returns error, new container log file MUST NOT
	// be created.
	ReopenContainerLog(ContainerID string, opts ...grpc.CallOption) error
	GetContainerEvents(ctx context.Context, request *runtimeapi.GetEventsRequest, opts ...grpc.CallOption) (runtimeapi.RuntimeService_GetContainerEventsClient, error)
}

// PodSandboxManager contains methods for operating on PodSandboxes. The methods
// are thread-safe.
type PodSandboxManager interface {
	// RunPodSandbox creates and starts a pod-level sandbox. Runtimes should ensure
	// the sandbox is in ready state.
	RunPodSandbox(config *runtimeapi.PodSandboxConfig, runtimeHandler string, opts ...grpc.CallOption) (string, error)
	// UpdatePodSandboxResources updates the cgroup resources for the sandbox.
	UpdatePodSandboxResources(podSandboxID string, overhead *runtimeapi.LinuxContainerResources, resources *runtimeapi.LinuxContainerResources, opts ...grpc.CallOption) error
	// StopPodSandbox stops the sandbox. If there are any running containers in the
	// sandbox, they should be force terminated.
	StopPodSandbox(podSandboxID string, opts ...grpc.CallOption) error
	// RemovePodSandbox removes the sandbox. If there are running containers in the
	// sandbox, they should be forcibly removed.
	RemovePodSandbox(podSandboxID string, opts ...grpc.CallOption) error
	// PodSandboxStatus returns the Status of the PodSandbox.
	PodSandboxStatus(podSandboxID string, opts ...grpc.CallOption) (*runtimeapi.PodSandboxStatus, error)
	// ListPodSandbox returns a list of Sandbox.
	ListPodSandbox(filter *runtimeapi.PodSandboxFilter, opts ...grpc.CallOption) ([]*runtimeapi.PodSandbox, error)
	// PortForward prepares a streaming endpoint to forward ports from a PodSandbox, and returns the address.
	PortForward(req *runtimeapi.PortForwardRequest, opts ...grpc.CallOption) (*runtimeapi.PortForwardResponse, error)
}

// ContainerStatsManager contains methods for retrieving the container
// statistics.
type ContainerStatsManager interface {
	// ContainerStats returns stats of the container. If the container does not
	// exist, the call returns an error.
	ContainerStats(containerID string, opts ...grpc.CallOption) (*runtimeapi.ContainerStats, error)
	// ListContainerStats returns stats of all running containers.
	ListContainerStats(filter *runtimeapi.ContainerStatsFilter, opts ...grpc.CallOption) ([]*runtimeapi.ContainerStats, error)
}

// RuntimeService interface should be implemented by a container runtime.
// The methods should be thread-safe.
type RuntimeService interface {
	RuntimeVersioner
	ContainerManager
	PodSandboxManager
	ContainerStatsManager

	// UpdateRuntimeConfig updates runtime configuration if specified
	UpdateRuntimeConfig(runtimeConfig *runtimeapi.RuntimeConfig, opts ...grpc.CallOption) error
	// Status returns the status of the runtime.
	Status(opts ...grpc.CallOption) (*runtimeapi.StatusResponse, error)
	// RuntimeConfig returns configuration information of the runtime.
	// A couple of notes:
	//   - The RuntimeConfigRequest object is not to be confused with the contents of UpdateRuntimeConfigRequest.
	//     The former is for having runtime tell Kubelet what to do, the latter vice versa.
	//   - It is the expectation of the Kubelet that these fields are static for the lifecycle of the Kubelet.
	//     The Kubelet will not re-request the RuntimeConfiguration after startup, and CRI implementations should
	//     avoid updating them without a full node reboot.
	RuntimeConfig(in *runtimeapi.RuntimeConfigRequest, opts ...grpc.CallOption) (*runtimeapi.RuntimeConfigResponse, error)
}

// ImageManagerService interface should be implemented by a container image
// manager.
// The methods should be thread-safe.
type ImageManagerService interface {
	// ListImages lists the existing images.
	ListImages(filter *runtimeapi.ImageFilter, opts ...grpc.CallOption) ([]*runtimeapi.Image, error)
	// ImageStatus returns the status of the image.
	ImageStatus(image *runtimeapi.ImageSpec, opts ...grpc.CallOption) (*runtimeapi.Image, error)
	// PullImage pulls an image with the authentication config.
	PullImage(image *runtimeapi.ImageSpec, auth *runtimeapi.AuthConfig, podSandboxConfig *runtimeapi.PodSandboxConfig, runtimeHandler string, opts ...grpc.CallOption) (string, error)
	// RemoveImage removes the image.
	RemoveImage(image *runtimeapi.ImageSpec, opts ...grpc.CallOption) error
	// ImageFsInfo returns information of the filesystem that is used to store images.
	ImageFsInfo(opts ...grpc.CallOption) ([]*runtimeapi.FilesystemUsage, error)
}
