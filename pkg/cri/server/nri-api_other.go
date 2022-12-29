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

package server

import (
	"context"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	cstore "github.com/containerd/containerd/pkg/cri/store/container"
	sstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/opencontainers/runtime-spec/specs-go"

	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/pkg/nri"

	"github.com/containerd/nri/pkg/api"
)

type nriAPI struct {
	cri *criService
	nri nri.API
}

func (a *nriAPI) register() {
}

func (a *nriAPI) isEnabled() bool {
	return false
}

//
// CRI-NRI lifecycle hook no-op interface
//

func (*nriAPI) runPodSandbox(context.Context, *sstore.Sandbox) error {
	return nil
}

func (*nriAPI) stopPodSandbox(context.Context, *sstore.Sandbox) error {
	return nil
}

func (*nriAPI) removePodSandbox(context.Context, *sstore.Sandbox) error {
	return nil
}

func (*nriAPI) postCreateContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*nriAPI) startContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*nriAPI) postStartContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*nriAPI) stopContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*nriAPI) removeContainer(context.Context, *sstore.Sandbox, *cstore.Container) error {
	return nil
}

func (*nriAPI) undoCreateContainer(context.Context, *sstore.Sandbox, string, *specs.Spec) {
}

func (*nriAPI) WithContainerAdjustment() containerd.NewContainerOpts {
	return func(ctx context.Context, _ *containerd.Client, c *containers.Container) error {
		return nil
	}
}

func (*nriAPI) WithContainerExit(*cstore.Container) containerd.ProcessDeleteOpts {
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

func (*nriAPI) GetName() string {
	return nriDomain
}

func (*nriAPI) ListPodSandboxes() []nri.PodSandbox {
	return nil
}

func (*nriAPI) ListContainers() []nri.Container {
	return nil
}

func (*nriAPI) GetPodSandbox(string) (nri.PodSandbox, bool) {
	return nil, false
}

func (*nriAPI) GetContainer(string) (nri.Container, bool) {
	return nil, false
}

func (*nriAPI) UpdateContainer(context.Context, *api.ContainerUpdate) error {
	return nil
}

func (*nriAPI) EvictContainer(context.Context, *api.ContainerEviction) error {
	return nil
}
