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

package pause

import (
	"context"

	"github.com/containerd/containerd"
	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/oci"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	osinterface "github.com/containerd/containerd/pkg/os"
	"github.com/containerd/containerd/sandbox"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// CRIService interface contains things required by controller, but not yet refactored from criService.
// This will be removed in subsequent iterations.
type CRIService interface {
	EnsureImageExists(ctx context.Context, ref string, config *runtime.PodSandboxConfig) (*imagestore.Image, error)
	StartSandboxExitMonitor(ctx context.Context, id string, pid uint32, exitCh <-chan containerd.ExitStatus) <-chan struct{}
}

type Controller struct {
	// config contains all configurations.
	config criconfig.Config
	// client is an instance of the containerd client
	client *containerd.Client
	// sandboxStore stores all resources associated with sandboxes.
	sandboxStore *sandboxstore.Store
	// os is an interface for all required os operations.
	os osinterface.OS
	// cri is CRI service that provides missing gaps needed by controller.
	cri CRIService
	// baseOCISpecs contains cached OCI specs loaded via `Runtime.BaseRuntimeSpec`
	baseOCISpecs map[string]*oci.Spec
}

func NewPause(
	config criconfig.Config,
	client *containerd.Client,
	sandboxStore *sandboxstore.Store,
	os osinterface.OS,
	cri CRIService,
	baseOCISpecs map[string]*oci.Spec,
) *Controller {
	return &Controller{
		config:       config,
		client:       client,
		sandboxStore: sandboxStore,
		os:           os,
		cri:          cri,
		baseOCISpecs: baseOCISpecs,
	}
}

var _ sandbox.Controller = (*Controller)(nil)

func (c *Controller) Shutdown(ctx context.Context, sandboxID string) error {
	//TODO implement me
	panic("implement me")
}

func (c *Controller) Wait(ctx context.Context, sandboxID string) (*api.ControllerWaitResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *Controller) Status(ctx context.Context, sandboxID string) (*api.ControllerStatusResponse, error) {
	//TODO implement me
	panic("implement me")
}
