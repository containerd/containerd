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

	"github.com/containerd/errdefs"
	"github.com/containerd/go-cni"
	"github.com/containerd/platforms"

	"github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/internal/cri/store/label"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	servertesting "github.com/containerd/containerd/v2/internal/cri/testing"
	"github.com/containerd/containerd/v2/internal/registrar"
	"github.com/containerd/containerd/v2/pkg/oci"
	ostesting "github.com/containerd/containerd/v2/pkg/os/testing"
)

type fakeSandboxService struct{}

func (f *fakeSandboxService) CreateSandbox(ctx context.Context, info sandbox.Sandbox, opts ...sandbox.CreateOpt) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeSandboxService) StartSandbox(ctx context.Context, sandboxer string, sandboxID string) (sandbox.ControllerInstance, error) {
	return sandbox.ControllerInstance{}, errdefs.ErrNotImplemented
}

func (f *fakeSandboxService) StopSandbox(ctx context.Context, sandboxer, sandboxID string, opts ...sandbox.StopOpt) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeSandboxService) ShutdownSandbox(ctx context.Context, sandboxer string, sandboxID string) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeSandboxService) WaitSandbox(ctx context.Context, sandboxer string, sandboxID string) (<-chan containerd.ExitStatus, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeSandboxService) SandboxStatus(ctx context.Context, sandboxer string, sandboxID string, verbose bool) (sandbox.ControllerStatus, error) {
	return sandbox.ControllerStatus{}, errdefs.ErrNotImplemented
}

func (f *fakeSandboxService) SandboxPlatform(ctx context.Context, sandboxer string, sandboxID string) (platforms.Platform, error) {
	return platforms.DefaultSpec(), nil
}

func (f *fakeSandboxService) SandboxController(sandboxer string) (sandbox.Controller, error) {
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

type fakeRuntimeService struct {
	ocispecs map[string]*oci.Spec
}

func (f fakeRuntimeService) Config() criconfig.Config {
	return testConfig
}

func (f fakeRuntimeService) LoadOCISpec(filename string) (*oci.Spec, error) {
	spec, ok := f.ocispecs[filename]
	if !ok {
		return nil, errdefs.ErrNotFound
	}
	return spec, nil
}

type testOpt func(*criService)

func withRuntimeService(rs RuntimeService) testOpt {
	return func(service *criService) {
		service.RuntimeService = rs
	}
}

// newTestCRIService creates a fake criService for test.
func newTestCRIService(opts ...testOpt) *criService {
	labels := label.NewStore()
	service := &criService{
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
	for _, opt := range opts {
		opt(service)
	}
	if service.RuntimeService == nil {
		service.RuntimeService = &fakeRuntimeService{}
	}
	if service.ImageService == nil {
		service.ImageService = &fakeImageService{}
	}

	return service
}
