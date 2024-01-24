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

package podsandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	eventtypes "github.com/containerd/containerd/v2/api/events"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/pkg/cri/server/podsandbox/types"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/pkg/oci"
	osinterface "github.com/containerd/containerd/v2/pkg/os"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.SandboxControllerPlugin,
		ID:   "podsandbox",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.LeasePlugin,
			plugins.SandboxStorePlugin,
			plugins.CRIServicePlugin,
			plugins.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			client, err := containerd.New(
				"",
				containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
				containerd.WithDefaultPlatform(platforms.Default()),
				containerd.WithInMemoryServices(ic),
			)
			if err != nil {
				return nil, fmt.Errorf("unable to init client for podsandbox: %w", err)
			}

			// Get runtime service.
			criRuntimePlugin, err := ic.GetByID(plugins.CRIServicePlugin, "runtime")
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI runtime service plugin dependency: %w", err)
			}
			runtimeService := criRuntimePlugin.(RuntimeService)

			// Get image service.
			criImagePlugin, err := ic.GetByID(plugins.CRIServicePlugin, "images")
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI image service plugin dependency: %w", err)
			}

			c := Controller{
				client:         client,
				config:         runtimeService.Config(),
				os:             osinterface.RealOS{},
				runtimeService: runtimeService,
				imageService:   criImagePlugin.(ImageService),
				store:          NewStore(),
			}
			return &c, nil
		},
	})
}

// CRIService interface contains things required by controller, but not yet refactored from criService.
// TODO: this will be removed in subsequent iterations.
type CRIService interface {
	// TODO: we should implement Event backoff in Controller.
	BackOffEvent(id string, event interface{})
}

// RuntimeService specifies dependencies to CRI runtime service.
type RuntimeService interface {
	Config() criconfig.Config
	LoadOCISpec(string) (*oci.Spec, error)
}

// ImageService specifies dependencies to CRI image service.
type ImageService interface {
	LocalResolve(refOrID string) (imagestore.Image, error)
	GetImage(id string) (imagestore.Image, error)
	PullImage(ctx context.Context, name string, creds func(string) (string, string, error), sc *runtime.PodSandboxConfig) (string, error)
	RuntimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string
	PinnedImage(string) string
}

type Controller struct {
	// config contains all configurations.
	config criconfig.Config
	// client is an instance of the containerd client
	client *containerd.Client
	// runtimeService is a dependency to CRI runtime service.
	runtimeService RuntimeService
	// imageService is a dependency to CRI image service.
	imageService ImageService
	// os is an interface for all required os operations.
	os osinterface.OS
	// cri is CRI service that provides missing gaps needed by controller.
	cri CRIService

	store *Store
}

func (c *Controller) Init(
	cri CRIService,
) {
	c.cri = cri
}

var _ sandbox.Controller = (*Controller)(nil)

func (c *Controller) Platform(_ctx context.Context, _sandboxID string) (platforms.Platform, error) {
	return platforms.DefaultSpec(), nil
}

func (c *Controller) Wait(ctx context.Context, sandboxID string) (sandbox.ExitStatus, error) {
	podSandbox := c.store.Get(sandboxID)
	if podSandbox == nil {
		return sandbox.ExitStatus{}, fmt.Errorf("failed to get exit channel. %q", sandboxID)

	}
	exit, err := podSandbox.Wait(ctx)
	if err != nil {
		return sandbox.ExitStatus{}, fmt.Errorf("failed to wait pod sandbox, %w", err)
	}
	return sandbox.ExitStatus{
		ExitStatus: exit.ExitCode(),
		ExitedAt:   exit.ExitTime(),
	}, err

}

func (c *Controller) waitSandboxExit(ctx context.Context, p *types.PodSandbox, exitCh <-chan containerd.ExitStatus) (exitStatus uint32, exitedAt time.Time, err error) {
	select {
	case e := <-exitCh:
		exitStatus, exitedAt, err = e.Result()
		if err != nil {
			log.G(ctx).WithError(err).Errorf("failed to get task exit status for %q", p.ID)
			exitStatus = unknownExitCode
			exitedAt = time.Now()
		}
		dctx := ctrdutil.NamespacedContext()
		dctx, dcancel := context.WithTimeout(dctx, handleEventTimeout)
		defer dcancel()
		event := &eventtypes.TaskExit{ExitStatus: exitStatus, ExitedAt: protobuf.ToTimestamp(exitedAt)}
		if cleanErr := handleSandboxTaskExit(dctx, p, event); cleanErr != nil {
			c.cri.BackOffEvent(p.ID, e)
		}
		return
	case <-ctx.Done():
		return unknownExitCode, time.Now(), ctx.Err()
	}
}

// handleSandboxTaskExit handles TaskExit event for sandbox.
func handleSandboxTaskExit(ctx context.Context, sb *types.PodSandbox, e *eventtypes.TaskExit) error {
	// No stream attached to sandbox container.
	task, err := sb.Container.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to load task for sandbox: %w", err)
		}
	} else {
		// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
		if _, err = task.Delete(ctx, WithNRISandboxDelete(sb.ID), containerd.WithProcessKill); err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to stop sandbox: %w", err)
			}
		}
	}
	return nil
}
