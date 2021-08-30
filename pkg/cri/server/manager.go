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
	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtime_alpha "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/store"
	cristore "github.com/containerd/containerd/pkg/cri/store/service"
)

type criManager struct {
	// stores all resources associated with cri
	*cristore.Store
	// config contains all configurations.
	config *criconfig.Config
	// services contains all cri plugins
	services map[string]CRIPlugin
	// default service
	c *criService
}

// NewCRIManager returns a new instance of CRIService
func NewCRIManager(config criconfig.Config, client *containerd.Client, store *cristore.Store, services map[string]CRIPlugin) (CRIService, error) {
	c, err := newCRIService(&config, client, store)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create CRI service")
	}
	for _, cri := range services {
		cri.SetDelegate(c)
		cri.SetConfig(&config)
	}
	cm := &criManager{
		Store:    store,
		config:   &config,
		c:        c,
		services: services,
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
	// run services
	for handler, s := range cm.services {
		go func(handler string, service CRIPlugin) {
			if err := service.Run(); err != nil {
				logrus.Fatalf("Failed to run CRI %s service", handler)
			}
		}(handler, s)
	}
	return cm.c.Run()
}

// implement CRIPlugin Initialized interface
func (cm *criManager) Initialized() bool {
	initialized := true
	for _, s := range cm.services {
		initialized = initialized && s.Initialized()
	}
	return initialized && cm.c.Initialized()
}

// Close stops the CRI service.
func (cm *criManager) Close() error {
	for handler, s := range cm.services {
		if err := s.Close(); err != nil {
			logrus.Errorf("Failed to Close CRI %s service", handler)
		}
	}
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

// getServiceByRuntime get runtime specific implementations
func (cm *criManager) getServiceByRuntime(runtime string) (GrpcServices, error) {
	r, ok := cm.config.ContainerdConfig.Runtimes[runtime]
	if !ok {
		return cm.c, nil
	}
	if len(r.CRIHandler) == 0 || r.CRIHandler == criconfig.DefaultCRIHandler {
		return cm.c, nil
	}
	return cm.getServiceByCRIHandler(r.CRIHandler)
}

func (cm *criManager) getServiceByCRIHandler(criHandler string) (GrpcServices, error) {
	i, ok := cm.services[criHandler]
	if !ok {
		// there is not plugin
		return nil, errors.Errorf("the handler %q not implement plugin", criHandler)
	}
	return i, nil
}

func (cm *criManager) getServiceBySandboxID(id string) (GrpcServices, error) {
	sandbox, err := cm.SandboxStore.Get(id)
	if err != nil {
		return nil, err
	}
	if len(sandbox.CRIHandler) == 0 || sandbox.CRIHandler == criconfig.DefaultCRIHandler {
		return cm.c, nil
	}
	return cm.getServiceByCRIHandler(sandbox.CRIHandler)
}

func (cm *criManager) getServiceByContainerID(id string) (GrpcServices, error) {
	container, err := cm.ContainerStore.Get(id)
	if err != nil {
		return nil, err
	}
	return cm.getServiceBySandboxID(container.SandboxID)
}

func (cm *criManager) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error) {
	i, err := cm.getServiceByRuntime(r.GetRuntimeHandler())
	if err != nil {
		return nil, errors.Wrapf(err, "no cir handler for %q is configured", r.GetRuntimeHandler())
	}
	return i.RunPodSandbox(ctx, r)
}

func (cm *criManager) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (*runtime.ListPodSandboxResponse, error) {
	return cm.c.ListPodSandbox(ctx, r)
}

func (cm *criManager) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (*runtime.PodSandboxStatusResponse, error) {
	i, err := cm.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
	}
	return i.PodSandboxStatus(ctx, r)
}

func (cm *criManager) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	i, err := cm.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
	}
	return i.StopPodSandbox(ctx, r)
}

func (cm *criManager) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	i, err := cm.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		if err != store.ErrNotExist {
			return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
		}
		// Do not return error if the service doesn't exist.
		log.G(ctx).Tracef("RemovePodSandbox called for sandbox %q that does not exist",
			r.GetPodSandboxId())
		return &runtime.RemovePodSandboxResponse{}, nil
	}
	return i.RemovePodSandbox(ctx, r)
}

func (cm *criManager) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (*runtime.PortForwardResponse, error) {
	i, err := cm.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
	}
	return i.PortForward(ctx, r)
}

func (cm *criManager) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (*runtime.CreateContainerResponse, error) {
	i, err := cm.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
	}
	return i.CreateContainer(ctx, r)
}

func (cm *criManager) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (*runtime.StartContainerResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.StartContainer(ctx, r)
}

func (cm *criManager) ListContainers(ctx context.Context, r *runtime.ListContainersRequest) (*runtime.ListContainersResponse, error) {
	return cm.c.ListContainers(ctx, r)
}

func (cm *criManager) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (*runtime.ContainerStatusResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.ContainerStatus(ctx, r)
}

func (cm *criManager) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (*runtime.StopContainerResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.StopContainer(ctx, r)
}

func (cm *criManager) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (*runtime.RemoveContainerResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		if err != store.ErrNotExist {
			return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
		}
		log.G(ctx).Tracef("RemoveContainer called for container %q that does not exist", r.GetContainerId())
		return &runtime.RemoveContainerResponse{}, nil
	}
	return i.RemoveContainer(ctx, r)
}

func (cm *criManager) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (*runtime.ExecSyncResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.ExecSync(ctx, r)
}

func (cm *criManager) Exec(ctx context.Context, r *runtime.ExecRequest) (*runtime.ExecResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.Exec(ctx, r)
}

func (cm *criManager) Attach(ctx context.Context, r *runtime.AttachRequest) (*runtime.AttachResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.Attach(ctx, r)
}

func (cm *criManager) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (*runtime.UpdateContainerResourcesResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.UpdateContainerResources(ctx, r)
}

func (cm *criManager) PullImage(ctx context.Context, r *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	if r.GetSandboxConfig() == nil || r.GetSandboxConfig().GetMetadata() == nil {
		return cm.c.PullImage(ctx, r)
	}
	key := cm.SandboxNameIndex.GetKeyByName(MakeSandboxName(r.GetSandboxConfig().GetMetadata()))
	if len(key) == 0 {
		return cm.c.PullImage(ctx, r)
	}
	i, err := cm.getServiceBySandboxID(key)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", key)
	}
	return i.PullImage(ctx, r)
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

func (cm *criManager) ContainerStats(ctx context.Context, r *runtime.ContainerStatsRequest) (*runtime.ContainerStatsResponse, error) {
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.ContainerStats(ctx, r)
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
	i, err := cm.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.ReopenContainerLog(ctx, r)
}
