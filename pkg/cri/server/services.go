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
	"io"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtime_alpha "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/store"
	cristore "github.com/containerd/containerd/pkg/cri/store/service"
)

// DefaultCRIMode is default cri mode
// TODO(wllenyj): what name?
const DefaultCRIMode = "runc"

// grpcServices are all the grpc services provided by cri containerd.
type grpcServices interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

type grpcAlphaServices interface {
	runtime_alpha.RuntimeServiceServer
	runtime_alpha.ImageServiceServer
}

// CRIPlugin is the interface implement different CRI implementions
type CRIPlugin interface {
	// need implement recover
	Run() error
	// used by Grpc service
	Initialized() bool
	// io.Closer is used by containerd to gracefully stop cri service.
	io.Closer
	grpcServices
}

// CRIService is the interface implement CRI remote service server.
type CRIService interface {
	plugin.Service
	CRIPlugin
}

type criServices struct {
	config *criconfig.Config
	// services contains all cri plugins
	services map[string]CRIPlugin
	// stores all resources associated with cri
	store *cristore.Store
	// default service
	c *criService
}

// NewCRIServices returns a new instance of CRIService
func NewCRIServices(config criconfig.Config, client *containerd.Client, store *cristore.Store, services map[string]CRIPlugin) (CRIService, error) {
	c, err := newCRIService(&config, client, store)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create CRI service")
	}
	cs := &criServices{
		config:   &config,
		services: services,
		store:    store,
		c:        c,
	}
	return cs, nil
}

// Register registers all required services onto a specific grpc server.
// This is used by containerd cri plugin.
func (c *criServices) Register(s *grpc.Server) error {
	return c.register(s)
}

// RegisterTCP register all required services onto a GRPC server on TCP.
// This is used by containerd CRI plugin.
func (c *criServices) RegisterTCP(s *grpc.Server) error {
	if !c.config.DisableTCPService {
		return c.register(s)
	}
	return nil
}

// implement CRIPlugin Initialized interface
func (c *criServices) Initialized() bool {
	initialized := true
	for _, s := range c.services {
		initialized = initialized && s.Initialized()
	}
	return initialized && c.c.Initialized()
}

// Run starts the CRI service.
func (c *criServices) Run() error {
	for mode, s := range c.services {
		go func(mode string, service CRIPlugin) {
			if err := service.Run(); err != nil {
				logrus.Fatalf("Failed to run CRI %s service", mode)
			}
		}(mode, s)
	}
	return c.c.Run()
}

// Close stops the CRI service.
func (c *criServices) Close() error {
	for mode, s := range c.services {
		if err := s.Close(); err != nil {
			logrus.Errorf("Failed to Close CRI %s service", mode)
		}
	}
	return c.c.Close()
}

func (c *criServices) register(s *grpc.Server) error {
	instrumented := newInstrumentedService(c)
	runtime.RegisterRuntimeServiceServer(s, instrumented)
	runtime.RegisterImageServiceServer(s, instrumented)
	instrumentedAlpha := newInstrumentedAlphaService(c)
	runtime_alpha.RegisterRuntimeServiceServer(s, instrumentedAlpha)
	runtime_alpha.RegisterImageServiceServer(s, instrumentedAlpha)
	return nil
}

// getServiceByRuntime get runtime specific implementations
func (c *criServices) getServiceByRuntime(runtime string) (CRIPlugin, error) {
	r, ok := c.config.ContainerdConfig.Runtimes[runtime]
	if !ok {
		return c.c, nil
	}
	if len(r.Mode) == 0 || r.Mode == DefaultCRIMode {
		return c.c, nil
	}
	return c.getServiceByMode(r.Mode)
}

func (c *criServices) getServiceByMode(mode string) (CRIPlugin, error) {
	i, ok := c.services[mode]
	if !ok {
		// there is not plugin
		return nil, errors.Errorf("no service for %q", mode)
	}
	return i, nil
}

func (c *criServices) getServiceBySandboxID(id string) (CRIPlugin, error) {
	sandbox, err := c.store.SandboxStore.Get(id)
	if err != nil {
		return nil, err
	}
	if len(sandbox.GetMetadata().Mode) == 0 || sandbox.GetMetadata().Mode == DefaultCRIMode {
		return c.c, nil
	}
	return c.getServiceByMode(sandbox.GetMetadata().Mode)
}

func (c *criServices) getServiceByContainerID(id string) (CRIPlugin, error) {
	container, err := c.store.ContainerStore.Get(id)
	if err != nil {
		return nil, err
	}
	if len(container.Mode) == 0 || container.Mode == DefaultCRIMode {
		return c.c, nil
	}
	return c.getServiceByMode(container.Mode)
}

func (c *criServices) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error) {
	i, err := c.getServiceByRuntime(r.GetRuntimeHandler())
	if err != nil {
		return nil, errors.Wrapf(err, "no runtime mode for %q is configured", r.GetRuntimeHandler())
	}
	return i.RunPodSandbox(ctx, r)
}

func (c *criServices) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (*runtime.ListPodSandboxResponse, error) {
	return c.c.ListPodSandbox(ctx, r)
}

func (c *criServices) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (*runtime.PodSandboxStatusResponse, error) {
	i, err := c.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
	}
	return i.PodSandboxStatus(ctx, r)
}

func (c *criServices) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	i, err := c.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
	}
	return i.StopPodSandbox(ctx, r)
}

func (c *criServices) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	i, err := c.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		if err != store.ErrNotExist {
			return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
		}
		// Do not return error if the id doesn't exist.
		log.G(ctx).Tracef("RemovePodSandbox called for sandbox %q that does not exist",
			r.GetPodSandboxId())
		return &runtime.RemovePodSandboxResponse{}, nil
	}
	return i.RemovePodSandbox(ctx, r)
}

func (c *criServices) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (*runtime.PortForwardResponse, error) {
	i, err := c.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
	}
	return i.PortForward(ctx, r)
}

func (c *criServices) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (*runtime.CreateContainerResponse, error) {
	i, err := c.getServiceBySandboxID(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for sandbox %q", r.GetPodSandboxId())
	}
	return i.CreateContainer(ctx, r)
}

func (c *criServices) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (*runtime.StartContainerResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.StartContainer(ctx, r)
}

func (c *criServices) ListContainers(ctx context.Context, r *runtime.ListContainersRequest) (*runtime.ListContainersResponse, error) {
	return c.c.ListContainers(ctx, r)
}

func (c *criServices) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (*runtime.ContainerStatusResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.ContainerStatus(ctx, r)
}

func (c *criServices) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (*runtime.StopContainerResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.StopContainer(ctx, r)
}

func (c *criServices) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (*runtime.RemoveContainerResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		if err != store.ErrNotExist {
			return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
		}
		log.G(ctx).Tracef("RemoveContainer called for container %q that does not exist", r.GetContainerId())
		return &runtime.RemoveContainerResponse{}, nil
	}
	return i.RemoveContainer(ctx, r)
}

func (c *criServices) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (*runtime.ExecSyncResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.ExecSync(ctx, r)
}

func (c *criServices) Exec(ctx context.Context, r *runtime.ExecRequest) (*runtime.ExecResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.Exec(ctx, r)
}

func (c *criServices) Attach(ctx context.Context, r *runtime.AttachRequest) (*runtime.AttachResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.Attach(ctx, r)
}

func (c *criServices) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (*runtime.UpdateContainerResourcesResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.UpdateContainerResources(ctx, r)
}

func (c *criServices) PullImage(ctx context.Context, r *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	return c.c.PullImage(ctx, r)
}

func (c *criServices) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (*runtime.ListImagesResponse, error) {
	return c.c.ListImages(ctx, r)
}

func (c *criServices) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (*runtime.ImageStatusResponse, error) {
	return c.c.ImageStatus(ctx, r)
}

func (c *criServices) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	return c.c.RemoveImage(ctx, r)
}

func (c *criServices) ImageFsInfo(ctx context.Context, r *runtime.ImageFsInfoRequest) (*runtime.ImageFsInfoResponse, error) {
	return c.c.ImageFsInfo(ctx, r)
}

func (c *criServices) ContainerStats(ctx context.Context, r *runtime.ContainerStatsRequest) (*runtime.ContainerStatsResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.ContainerStats(ctx, r)
}

func (c *criServices) ListContainerStats(ctx context.Context, r *runtime.ListContainerStatsRequest) (*runtime.ListContainerStatsResponse, error) {
	return c.c.ListContainerStats(ctx, r)
}

func (c *criServices) Status(ctx context.Context, r *runtime.StatusRequest) (*runtime.StatusResponse, error) {
	return c.c.Status(ctx, r)
}

func (c *criServices) Version(ctx context.Context, r *runtime.VersionRequest) (*runtime.VersionResponse, error) {
	return c.c.Version(ctx, r)
}

func (c *criServices) UpdateRuntimeConfig(ctx context.Context, r *runtime.UpdateRuntimeConfigRequest) (*runtime.UpdateRuntimeConfigResponse, error) {
	return c.c.UpdateRuntimeConfig(ctx, r)
}

func (c *criServices) ReopenContainerLog(ctx context.Context, r *runtime.ReopenContainerLogRequest) (*runtime.ReopenContainerLogResponse, error) {
	i, err := c.getServiceByContainerID(r.GetContainerId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get service for container %q", r.GetContainerId())
	}
	return i.ReopenContainerLog(ctx, r)
}
