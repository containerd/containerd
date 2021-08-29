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

	"github.com/containerd/containerd"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtime_alpha "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
)

type criManager struct {
	// config contains all configurations.
	config *criconfig.Config
	// default service
	c *criService
}

// NewCRIManager returns a new instance of CRIService
func NewCRIManager(config criconfig.Config, client *containerd.Client) (CRIService, error) {
	c, err := newCRIService(&config, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create CRI service")
	}
	cm := &criManager{
		config: &config,
		c:      c,
	}
	return cm, nil
}

// Register registers all required services onto a specific grpc server.
// This is used by containerd cri plugin.
func (cm *criManager) Register(s *grpc.Server) error {
	return cm.register(s)
}

// RegisterTCP register all required services onto a GRPC server on TCP.
// This is used by containerd CRI plugin.
func (cm *criManager) RegisterTCP(s *grpc.Server) error {
	if !cm.config.DisableTCPService {
		return cm.register(s)
	}
	return nil
}

// Run starts the CRI service.
func (cm *criManager) Run() error {
	return cm.c.Run()
}

// Close stops the CRI service.
func (cm *criManager) Close() error {
	return cm.c.Close()
}

func (cm *criManager) register(s *grpc.Server) error {
	instrumented := newInstrumentedService(cm)
	runtime.RegisterRuntimeServiceServer(s, instrumented)
	runtime.RegisterImageServiceServer(s, instrumented)
	instrumentedAlpha := newInstrumentedAlphaService(cm)
	runtime_alpha.RegisterRuntimeServiceServer(s, instrumentedAlpha)
	runtime_alpha.RegisterImageServiceServer(s, instrumentedAlpha)
	return nil
}

func (cm *criManager) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error) {
	return cm.c.RunPodSandbox(ctx, r)
}

func (cm *criManager) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (*runtime.ListPodSandboxResponse, error) {
	return cm.c.ListPodSandbox(ctx, r)
}

func (cm *criManager) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (*runtime.PodSandboxStatusResponse, error) {
	return cm.c.PodSandboxStatus(ctx, r)
}

func (cm *criManager) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	return cm.c.StopPodSandbox(ctx, r)
}

func (cm *criManager) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	return cm.c.RemovePodSandbox(ctx, r)
}

func (cm *criManager) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (*runtime.PortForwardResponse, error) {
	return cm.c.PortForward(ctx, r)
}

func (cm *criManager) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (*runtime.CreateContainerResponse, error) {
	return cm.c.CreateContainer(ctx, r)
}

func (cm *criManager) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (*runtime.StartContainerResponse, error) {
	return cm.c.StartContainer(ctx, r)
}

func (cm *criManager) ListContainers(ctx context.Context, r *runtime.ListContainersRequest) (*runtime.ListContainersResponse, error) {
	return cm.c.ListContainers(ctx, r)
}

func (cm *criManager) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (*runtime.ContainerStatusResponse, error) {
	return cm.c.ContainerStatus(ctx, r)
}

func (cm *criManager) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (*runtime.StopContainerResponse, error) {
	return cm.c.StopContainer(ctx, r)
}

func (cm *criManager) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (*runtime.RemoveContainerResponse, error) {
	return cm.c.RemoveContainer(ctx, r)
}

func (cm *criManager) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (*runtime.ExecSyncResponse, error) {
	return cm.c.ExecSync(ctx, r)
}

func (cm *criManager) Exec(ctx context.Context, r *runtime.ExecRequest) (*runtime.ExecResponse, error) {
	return cm.c.Exec(ctx, r)
}

func (cm *criManager) Attach(ctx context.Context, r *runtime.AttachRequest) (*runtime.AttachResponse, error) {
	return cm.c.Attach(ctx, r)
}

func (cm *criManager) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (*runtime.UpdateContainerResourcesResponse, error) {
	return cm.c.UpdateContainerResources(ctx, r)
}

func (cm *criManager) PullImage(ctx context.Context, r *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	return cm.c.PullImage(ctx, r)
}

func (cm *criManager) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (*runtime.ListImagesResponse, error) {
	return cm.c.ListImages(ctx, r)
}

func (cm *criManager) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (*runtime.ImageStatusResponse, error) {
	return cm.c.ImageStatus(ctx, r)
}

func (cm *criManager) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	return cm.c.RemoveImage(ctx, r)
}

func (cm *criManager) ImageFsInfo(ctx context.Context, r *runtime.ImageFsInfoRequest) (*runtime.ImageFsInfoResponse, error) {
	return cm.c.ImageFsInfo(ctx, r)
}

func (cm *criManager) PodSandboxStats(ctx context.Context, r *runtime.PodSandboxStatsRequest) (*runtime.PodSandboxStatsResponse, error) {
	return cm.c.PodSandboxStats(ctx, r)
}

func (cm *criManager) ContainerStats(ctx context.Context, r *runtime.ContainerStatsRequest) (*runtime.ContainerStatsResponse, error) {
	return cm.c.ContainerStats(ctx, r)
}

func (cm *criManager) ListPodSandboxStats(ctx context.Context, r *runtime.ListPodSandboxStatsRequest) (res *runtime.ListPodSandboxStatsResponse, err error) {
	return cm.c.ListPodSandboxStats(ctx, r)
}

func (cm *criManager) ListContainerStats(ctx context.Context, r *runtime.ListContainerStatsRequest) (*runtime.ListContainerStatsResponse, error) {
	return cm.c.ListContainerStats(ctx, r)
}

func (cm *criManager) Status(ctx context.Context, r *runtime.StatusRequest) (*runtime.StatusResponse, error) {
	return cm.c.Status(ctx, r)
}

func (cm *criManager) Version(ctx context.Context, r *runtime.VersionRequest) (*runtime.VersionResponse, error) {
	return cm.c.Version(ctx, r)
}

func (cm *criManager) UpdateRuntimeConfig(ctx context.Context, r *runtime.UpdateRuntimeConfigRequest) (*runtime.UpdateRuntimeConfigResponse, error) {
	return cm.c.UpdateRuntimeConfig(ctx, r)
}

func (cm *criManager) ReopenContainerLog(ctx context.Context, r *runtime.ReopenContainerLogRequest) (*runtime.ReopenContainerLogResponse, error) {
	return cm.c.ReopenContainerLog(ctx, r)
}
