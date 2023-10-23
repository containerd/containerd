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
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/oci"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/pkg/cri/server/base"
	"github.com/containerd/containerd/v2/pkg/cri/server/images"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	osinterface "github.com/containerd/containerd/v2/pkg/os"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/sandbox"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.SandboxControllerPlugin,
		ID:   "podsandbox",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.LeasePlugin,
			plugins.SandboxStorePlugin,
			plugins.InternalPlugin,
			plugins.CRIImagePlugin,
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

			// Get base CRI dependencies.
			criBasePlugin, err := ic.GetByID(plugins.InternalPlugin, "cri")
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI service base dependencies: %w", err)
			}
			criBase := criBasePlugin.(*base.CRIBase)

			// Get image service.
			criImagePlugin, err := ic.GetByID(plugins.CRIImagePlugin, "cri-image-service")
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI image service plugin dependency: %w", err)
			}
			imageService := criImagePlugin.(*images.CRIImageService)

			c := Controller{
				client:       client,
				config:       criBase.Config,
				os:           osinterface.RealOS{},
				baseOCISpecs: criBase.BaseOCISpecs,
				imageService: imageService,
				store:        NewStore(),
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

// ImageService specifies dependencies to CRI image service.
type ImageService interface {
	runtime.ImageServiceServer

	LocalResolve(refOrID string) (imagestore.Image, error)
	GetImage(id string) (imagestore.Image, error)
}

type Controller struct {
	// config contains all configurations.
	config criconfig.Config
	// client is an instance of the containerd client
	client *containerd.Client
	// imageService is a dependency to CRI image service.
	imageService ImageService
	// sandboxStore stores all resources associated with sandboxes.
	sandboxStore *sandboxstore.Store
	// os is an interface for all required os operations.
	os osinterface.OS
	// cri is CRI service that provides missing gaps needed by controller.
	cri CRIService
	// baseOCISpecs contains cached OCI specs loaded via `Runtime.BaseRuntimeSpec`
	baseOCISpecs map[string]*oci.Spec

	store *Store
}

func (c *Controller) Init(
	sandboxStore *sandboxstore.Store,
	cri CRIService,
) {
	c.cri = cri
	c.sandboxStore = sandboxStore
}

var _ sandbox.Controller = (*Controller)(nil)

func (c *Controller) Platform(_ctx context.Context, _sandboxID string) (platforms.Platform, error) {
	return platforms.DefaultSpec(), nil
}

func (c *Controller) Wait(ctx context.Context, sandboxID string) (sandbox.ExitStatus, error) {
	status := c.store.Get(sandboxID)
	if status == nil {
		return sandbox.ExitStatus{}, fmt.Errorf("failed to get exit channel. %q", sandboxID)
	}

	exitStatus, exitedAt, err := c.waitSandboxExit(ctx, sandboxID, status.Waiter)

	return sandbox.ExitStatus{
		ExitStatus: exitStatus,
		ExitedAt:   exitedAt,
	}, err
}

func (c *Controller) waitSandboxExit(ctx context.Context, id string, exitCh <-chan containerd.ExitStatus) (exitStatus uint32, exitedAt time.Time, err error) {
	exitStatus = unknownExitCode
	exitedAt = time.Now()
	select {
	case exitRes := <-exitCh:
		log.G(ctx).Debugf("received sandbox exit %+v", exitRes)

		exitStatus, exitedAt, err = exitRes.Result()
		if err != nil {
			log.G(ctx).WithError(err).Errorf("failed to get task exit status for %q", id)
			exitStatus = unknownExitCode
			exitedAt = time.Now()
		}

		err = func() error {
			dctx := ctrdutil.NamespacedContext()
			dctx, dcancel := context.WithTimeout(dctx, handleEventTimeout)
			defer dcancel()

			sb, err := c.sandboxStore.Get(id)
			if err == nil {
				if err := handleSandboxExit(dctx, sb, &eventtypes.TaskExit{ExitStatus: exitStatus, ExitedAt: protobuf.ToTimestamp(exitedAt)}); err != nil {
					return err
				}
				return nil
			} else if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to get sandbox %s: %w", id, err)
			}
			return nil
		}()
		if err != nil {
			log.G(ctx).WithError(err).Errorf("failed to handle sandbox TaskExit %s", id)
			// Don't backoff, the caller is responsible for.
			return
		}
	case <-ctx.Done():
		return exitStatus, exitedAt, ctx.Err()
	}
	return
}

// handleSandboxExit handles TaskExit event for sandbox.
// TODO https://github.com/containerd/containerd/issues/7548
func handleSandboxExit(ctx context.Context, sb sandboxstore.Sandbox, e *eventtypes.TaskExit) error {
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
			// Move on to make sure container status is updated.
		}
	}
	sb.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		status.State = sandboxstore.StateNotReady
		status.Pid = 0
		status.ExitStatus = e.ExitStatus
		status.ExitedAt = e.ExitedAt.AsTime()
		return status, nil
	})
	// Using channel to propagate the information of sandbox stop
	sb.Stop()
	return nil
}
