//go:build !linux

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

package nri

import (
	"context"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	cstore "github.com/containerd/containerd/pkg/cri/store/container"
	sstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/opencontainers/runtime-spec/specs-go"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/pkg/nri"

	"github.com/containerd/nri/pkg/api"
)

type API struct {
}

func NewAPI(nri.API) *API {
	return nil
}

func (a *API) Register(CRIImplementation) error {
	return nil
}

func (a *API) IsEnabled() bool {
	return false
}

//
// CRI-NRI lifecycle hook no-op interface
//

func (*API) RunPodSandbox(context.Context, *sstore.Sandbox) error {
	return nil
}

func (*API) StopPodSandbox(context.Context, *sstore.Sandbox) error {
	return nil
}

func (*API) RemovePodSandbox(context.Context, *sstore.Sandbox) error {
	return nil
}

func (*API) PostCreateContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*API) StartContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*API) PostStartContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*API) UpdateContainerResources(context.Context, *sstore.Sandbox, *cstore.Container, *cri.LinuxContainerResources) (*cri.LinuxContainerResources, error) {
	return nil, nil
}

func (*API) PostUpdateContainerResources(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*API) StopContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*API) RemoveContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*API) UndoCreateContainer(context.Context, *sstore.Sandbox, string, *specs.Spec) {
}

func (*API) WithContainerAdjustment() containerd.NewContainerOpts {
	return func(ctx context.Context, _ *containerd.Client, c *containers.Container) error {
		return nil
	}
}

func (*API) WithContainerExit(*cstore.Container) containerd.ProcessDeleteOpts {
	return func(_ context.Context, _ containerd.Process) error {
		return nil
	}
}

//
// NRI-CRI no-op 'domain' interface
//

const (
	nriDomain = constants.K8sContainerdNamespace
)

func (*API) GetName() string {
	return nriDomain
}

func (*API) ListPodSandboxes() []nri.PodSandbox {
	return nil
}

func (*API) ListContainers() []nri.Container {
	return nil
}

func (*API) GetPodSandbox(string) (nri.PodSandbox, bool) {
	return nil, false
}

func (*API) GetContainer(string) (nri.Container, bool) {
	return nil, false
}

func (*API) UpdateContainer(context.Context, *api.ContainerUpdate) error {
	return nil
}

func (*API) EvictContainer(context.Context, *api.ContainerEviction) error {
	return nil
}
