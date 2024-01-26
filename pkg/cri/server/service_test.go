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

	"github.com/containerd/go-cni"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/api/types"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/registrar"
	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
	"github.com/containerd/containerd/v2/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	servertesting "github.com/containerd/containerd/v2/pkg/cri/testing"
	ostesting "github.com/containerd/containerd/v2/pkg/os/testing"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
)

type fakeSandboxService struct{}

func (f *fakeSandboxService) SandboxController(config *runtime.PodSandboxConfig, runtimeHandler string) (sandbox.Controller, error) {
	return &fakeSandboxController{}, nil
}

type fakeSandboxController struct{}

func (f fakeSandboxController) Create(_ctx context.Context, _sandboxInfo sandbox.Sandbox, _opts ...sandbox.CreateOpt) error {
	return errdefs.ErrNotImplemented
}

func (f fakeSandboxController) Start(ctx context.Context, sandboxID string) (sandbox.ControllerInstance, error) {
	return sandbox.ControllerInstance{}, errdefs.ErrNotImplemented
}

func (f fakeSandboxController) Platform(_ctx context.Context, _sandboxID string) (platforms.Platform, error) {
	return platforms.DefaultSpec(), nil
}

func (f fakeSandboxController) Stop(_ctx context.Context, _sandboxID string, _opts ...sandbox.StopOpt) error {
	return errdefs.ErrNotImplemented
}

func (f fakeSandboxController) Wait(_ctx context.Context, _sandboxID string) (sandbox.ExitStatus, error) {
	return sandbox.ExitStatus{}, errdefs.ErrNotImplemented
}

func (f fakeSandboxController) Status(_ctx context.Context, sandboxID string, _verbose bool) (sandbox.ControllerStatus, error) {
	return sandbox.ControllerStatus{}, errdefs.ErrNotImplemented
}

func (f fakeSandboxController) Shutdown(ctx context.Context, sandboxID string) error {
	return errdefs.ErrNotImplemented
}

func (f fakeSandboxController) Metrics(ctx context.Context, sandboxID string) (*types.Metric, error) {
	return &types.Metric{}, errdefs.ErrNotImplemented
}

// newTestCRIService creates a fake criService for test.
func newTestCRIService() *criService {
	labels := label.NewStore()
	return &criService{
		ImageService:       &fakeImageService{},
		config:             testConfig,
		os:                 ostesting.NewFakeOS(),
		sandboxStore:       sandboxstore.NewStore(labels),
		sandboxNameIndex:   registrar.NewRegistrar(),
		containerStore:     containerstore.NewStore(labels),
		containerNameIndex: registrar.NewRegistrar(),
		netPlugin: map[string]cni.CNI{
			defaultNetworkPlugin: servertesting.NewFakeCNIPlugin(),
		},
		sandboxService: &fakeSandboxService{},
	}
}
