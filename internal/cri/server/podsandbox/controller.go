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
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	eventtypes "github.com/containerd/containerd/api/events"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/constants"
	"github.com/containerd/containerd/v2/internal/cri/server/events"
	"github.com/containerd/containerd/v2/internal/cri/server/podsandbox/types"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	osinterface "github.com/containerd/containerd/v2/pkg/os"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/services/warning"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.PodSandboxPlugin,
		ID:   "podsandbox",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.LeasePlugin,
			plugins.SandboxStorePlugin,
			plugins.TransferPlugin,
			plugins.CRIServicePlugin,
			plugins.ServicePlugin,
			plugins.WarningPlugin,
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

			// Get image service.
			criImagePlugin, err := ic.GetByID(plugins.CRIServicePlugin, "images")
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI image service plugin dependency: %w", err)
			}

			warningPlugin, err := ic.GetSingle(plugins.WarningPlugin)
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI warning service plugin dependency: %w", err)
			}

			c := Controller{
				client:         client,
				config:         criRuntimePlugin.(interface{ Config() criconfig.Config }).Config(),
				imageConfig:    criImagePlugin.(interface{ Config() criconfig.ImageConfig }).Config(),
				os:             osinterface.RealOS{},
				imageService:   criImagePlugin.(ImageService),
				warningService: warningPlugin.(warning.Service),
				store:          NewStore(),
			}

			// There is no need to subscribe to the exit event for the pause container,
			// as a dedicated goroutine already monitors the sandbox exit event.
			// The eventMonitor handles the backoff mechanism in case the pause container cleanup fails.
			c.eventMonitor = events.NewEventMonitor(
				&podSandboxEventHandler{
					controller: &c,
				},
			)
			c.eventMonitor.Start()
			return &c, nil
		},
	})
}

// ImageService specifies dependencies to CRI image service.
type ImageService interface {
	LocalResolve(refOrID string) (imagestore.Image, error)
	GetImage(id string) (imagestore.Image, error)
	PullImage(ctx context.Context, name string, creds func(string) (string, string, error), sc *runtime.PodSandboxConfig, runtimeHandler string) (string, error)
}

type Controller struct {
	// config contains all configurations.
	config criconfig.Config
	// imageConfig contains CRI image configuration.
	imageConfig criconfig.ImageConfig
	// client is an instance of the containerd client
	client *containerd.Client
	// imageService is a dependency to CRI image service.
	imageService ImageService
	// warningService is used to emit deprecation warnings.
	warningService warning.Service
	// os is an interface for all required os operations.
	os osinterface.OS
	// eventMonitor is the event monitor for podsandbox controller to handle sandbox task exit event
	// actually we only use it's backoff mechanism to make sure pause container is cleaned up.
	eventMonitor *events.EventMonitor

	store *Store
}

var _ sandbox.Controller = (*Controller)(nil)

func (c *Controller) Platform(_ctx context.Context, _sandboxID string) (imagespec.Platform, error) {
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

func (c *Controller) Update(
	ctx context.Context,
	sandboxID string,
	sandbox sandbox.Sandbox,
	fields ...string) error {
	return nil
}

func (c *Controller) waitSandboxExit(ctx context.Context, p *types.PodSandbox, exitCh <-chan containerd.ExitStatus) error {
	select {
	case e := <-exitCh:
		exitStatus, exitedAt, err := e.Result()
		if err != nil {
			log.G(ctx).WithError(err).Errorf("failed to get task exit status for %q", p.ID)
			exitStatus = unknownExitCode
			exitedAt = time.Now()
		}

		dctx := ctrdutil.NamespacedContext()
		dctx, dcancel := context.WithTimeout(dctx, handleEventTimeout)
		defer dcancel()

		event := &eventtypes.TaskExit{
			ID:          p.ID,
			ContainerID: p.ID,
			ExitStatus:  exitStatus,
			ExitedAt:    protobuf.ToTimestamp(exitedAt),
		}

		log.G(ctx).WithField("monitor_name", "podsandbox").
			Infof("received sandbox exit event %+v", event)

		if err := handleSandboxTaskExit(dctx, p, event); err != nil {
			log.G(ctx).WithError(err).Errorf("failed to handle sandbox exit event %+v", event)
			c.eventMonitor.Backoff(p.ID, event)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
	if err := sb.Exit(e.ExitStatus, protobuf.FromTimestamp(e.ExitedAt)); err != nil {
		return err
	}
	return nil
}
