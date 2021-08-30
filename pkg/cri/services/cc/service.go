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

package cc

import (
	"context"

	"github.com/containerd/containerd/plugin"
	"github.com/pkg/errors"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/server"
	cristore "github.com/containerd/containerd/pkg/cri/store/service"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.CRIPlugin,
		ID:   "cc",
		Requires: []plugin.Type{
			plugin.CRIServicePlugin,
		},
		InitFn: initCRICCService,
	})
}

func initCRICCService(ic *plugin.InitContext) (interface{}, error) {
	criStore, err := getCRIStore(ic)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get CRI store services")
	}
	cc := &ccService{
		Store: criStore,
	}
	return cc, nil
}

func getCRIStore(ic *plugin.InitContext) (*cristore.Store, error) {
	plugins, err := ic.GetByType(plugin.CRIServicePlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cri store")
	}
	p := plugins[cristore.CRIStoreService]
	if p == nil {
		return nil, errors.Errorf("cri service store not found")
	}
	i, err := p.Instance()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get instance of cri service store")
	}
	return i.(*cristore.Store), nil
}

type ccService struct {
	// stores all resources associated with cri
	*cristore.Store
	// config contains all configurations.
	config *criconfig.Config
	// default cir implemention
	delegate server.GrpcServices
}

func (cc *ccService) SetDelegate(delegate server.GrpcServices) {
	cc.delegate = delegate
}

func (cc *ccService) SetConfig(config *criconfig.Config) {
	cc.config = config
}

// implement CRIPlugin Initialized interface
func (cc *ccService) Initialized() bool {
	return true
}

// Run starts the CRI service.
func (cc *ccService) Run() error {
	return nil
}

// Close stops the CRI service.
func (cc *ccService) Close() error {
	return nil
}

func (cc *ccService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error) {
	return cc.delegate.RunPodSandbox(ctx, r)
}

func (cc *ccService) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (*runtime.ListPodSandboxResponse, error) {
	return cc.delegate.ListPodSandbox(ctx, r)
}

func (cc *ccService) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (*runtime.PodSandboxStatusResponse, error) {
	return cc.delegate.PodSandboxStatus(ctx, r)
}

func (cc *ccService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	return cc.delegate.StopPodSandbox(ctx, r)
}

func (cc *ccService) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	return cc.delegate.RemovePodSandbox(ctx, r)
}

func (cc *ccService) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (*runtime.PortForwardResponse, error) {
	return cc.delegate.PortForward(ctx, r)
}

func (cc *ccService) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (*runtime.CreateContainerResponse, error) {
	return cc.delegate.CreateContainer(ctx, r)
}

func (cc *ccService) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (*runtime.StartContainerResponse, error) {
	return cc.delegate.StartContainer(ctx, r)
}

func (cc *ccService) ListContainers(ctx context.Context, r *runtime.ListContainersRequest) (*runtime.ListContainersResponse, error) {
	return cc.delegate.ListContainers(ctx, r)
}

func (cc *ccService) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (*runtime.ContainerStatusResponse, error) {
	return cc.delegate.ContainerStatus(ctx, r)
}

func (cc *ccService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (*runtime.StopContainerResponse, error) {
	return cc.delegate.StopContainer(ctx, r)
}

func (cc *ccService) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (*runtime.RemoveContainerResponse, error) {
	return cc.delegate.RemoveContainer(ctx, r)
}

func (cc *ccService) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (*runtime.ExecSyncResponse, error) {
	return cc.delegate.ExecSync(ctx, r)
}

func (cc *ccService) Exec(ctx context.Context, r *runtime.ExecRequest) (*runtime.ExecResponse, error) {
	return cc.delegate.Exec(ctx, r)
}

func (cc *ccService) Attach(ctx context.Context, r *runtime.AttachRequest) (*runtime.AttachResponse, error) {
	return cc.delegate.Attach(ctx, r)
}

func (cc *ccService) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (*runtime.UpdateContainerResourcesResponse, error) {
	return cc.delegate.UpdateContainerResources(ctx, r)
}

func (cc *ccService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	return cc.delegate.PullImage(ctx, r)
}

func (cc *ccService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (*runtime.ListImagesResponse, error) {
	return cc.delegate.ListImages(ctx, r)
}

func (cc *ccService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (*runtime.ImageStatusResponse, error) {
	return cc.delegate.ImageStatus(ctx, r)
}

func (cc *ccService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	return cc.delegate.RemoveImage(ctx, r)
}

func (cc *ccService) ImageFsInfo(ctx context.Context, r *runtime.ImageFsInfoRequest) (*runtime.ImageFsInfoResponse, error) {
	return cc.delegate.ImageFsInfo(ctx, r)
}

func (cc *ccService) ContainerStats(ctx context.Context, r *runtime.ContainerStatsRequest) (*runtime.ContainerStatsResponse, error) {
	return cc.delegate.ContainerStats(ctx, r)
}

func (cc *ccService) ListContainerStats(ctx context.Context, r *runtime.ListContainerStatsRequest) (*runtime.ListContainerStatsResponse, error) {
	return cc.delegate.ListContainerStats(ctx, r)
}

func (cc *ccService) Status(ctx context.Context, r *runtime.StatusRequest) (*runtime.StatusResponse, error) {
	return cc.delegate.Status(ctx, r)
}

func (cc *ccService) Version(ctx context.Context, r *runtime.VersionRequest) (*runtime.VersionResponse, error) {
	return cc.delegate.Version(ctx, r)
}

func (cc *ccService) UpdateRuntimeConfig(ctx context.Context, r *runtime.UpdateRuntimeConfigRequest) (*runtime.UpdateRuntimeConfigResponse, error) {
	return cc.delegate.UpdateRuntimeConfig(ctx, r)
}

func (cc *ccService) ReopenContainerLog(ctx context.Context, r *runtime.ReopenContainerLogRequest) (*runtime.ReopenContainerLogResponse, error) {
	return cc.delegate.ReopenContainerLog(ctx, r)
}
