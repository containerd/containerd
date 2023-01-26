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
	"errors"
	"fmt"

	"github.com/containerd/nri"
	v1 "github.com/containerd/nri/types/v1"
	"github.com/containerd/typeurl"
	"github.com/davecgh/go-spew/spew"
	"github.com/opencontainers/selinux/go-selinux"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd"
	api "github.com/containerd/containerd/api/services/sandbox/v1"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/cri/annotations"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	customopts "github.com/containerd/containerd/pkg/cri/opts"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/containerd/snapshots"
)

func init() {
	typeurl.Register(&sandboxstore.Metadata{},
		"github.com/containerd/cri/pkg/store/sandbox", "Metadata")
}

// Start creates resources required for the sandbox and starts the sandbox.  If an error occurs, Start attempts to tear
// down the created resources.  If an error occurs while tearing down resources, a zero-valued response is returned
// alongside the error.  If the teardown was successful, a nil response is returned with the error.
// TODO(samuelkarp) Determine whether this error indication is reasonable to retain once controller.Delete is implemented.
func (c *Controller) Start(ctx context.Context, id string) (resp *api.ControllerStartResponse, retErr error) {
	var cleanupErr error
	defer func() {
		if retErr != nil && cleanupErr != nil {
			log.G(ctx).WithField("id", id).WithError(cleanupErr).Errorf("failed to fully teardown sandbox resources after earlier error: %s", retErr)
			resp = &api.ControllerStartResponse{}
		}
	}()

	sandboxInfo, err := c.client.SandboxStore().Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("unable to find sandbox with id %q: %w", id, err)
	}

	var metadata sandboxstore.Metadata
	if err := sandboxInfo.GetExtension(MetadataKey, &metadata); err != nil {
		return nil, fmt.Errorf("failed to get sandbox %q metadata: %w", id, err)
	}

	var (
		config = metadata.Config
		labels = map[string]string{}
	)

	// Ensure sandbox container image snapshot.
	image, err := c.cri.EnsureImageExists(ctx, c.config.SandboxImage, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox image %q: %w", c.config.SandboxImage, err)
	}

	containerdImage, err := c.toContainerdImage(ctx, *image)
	if err != nil {
		return nil, fmt.Errorf("failed to get image from containerd %q: %w", image.ID, err)
	}

	ociRuntime, err := c.getSandboxRuntime(config, metadata.RuntimeHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox runtime: %w", err)
	}
	log.G(ctx).WithField("podsandboxid", id).Debugf("use OCI runtime %+v", ociRuntime)

	labels["oci_runtime_type"] = ociRuntime.Type

	// Create sandbox container.
	// NOTE: sandboxContainerSpec SHOULD NOT have side
	// effect, e.g. accessing/creating files, so that we can test
	// it safely.
	spec, err := c.sandboxContainerSpec(id, config, &image.ImageSpec.Config, metadata.NetNSPath, ociRuntime.PodAnnotations)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sandbox container spec: %w", err)
	}
	log.G(ctx).WithField("podsandboxid", id).Debugf("sandbox container spec: %#+v", spew.NewFormatter(spec))

	metadata.ProcessLabel = spec.Process.SelinuxLabel
	defer func() {
		if retErr != nil {
			selinux.ReleaseLabel(metadata.ProcessLabel)
		}
	}()
	labels["selinux_label"] = metadata.ProcessLabel

	// handle any KVM based runtime
	if err := modifyProcessLabel(ociRuntime.Type, spec); err != nil {
		return nil, err
	}

	if config.GetLinux().GetSecurityContext().GetPrivileged() {
		// If privileged don't set selinux label, but we still record the MCS label so that
		// the unused label can be freed later.
		spec.Process.SelinuxLabel = ""
	}

	// Generate spec options that will be applied to the spec later.
	specOpts, err := c.sandboxContainerSpecOpts(config, &image.ImageSpec.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sandbox container spec options: %w", err)
	}

	sandboxLabels := buildLabels(config.Labels, image.ImageSpec.Config.Labels, containerKindSandbox)

	snapshotterOpt := snapshots.WithLabels(snapshots.FilterInheritedLabels(config.Annotations))
	opts := []containerd.NewContainerOpts{
		containerd.WithSnapshotter(c.runtimeSnapshotter(ctx, ociRuntime)),
		customopts.WithNewSnapshot(id, containerdImage, snapshotterOpt),
		containerd.WithSpec(spec, specOpts...),
		containerd.WithContainerLabels(sandboxLabels),
		containerd.WithContainerExtension(sandboxMetadataExtension, &metadata),
		containerd.WithRuntime(ociRuntime.Type, sandboxInfo.Runtime.Options),
	}

	container, err := c.client.NewContainer(ctx, id, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd container: %w", err)
	}
	defer func() {
		if retErr != nil && cleanupErr == nil {
			deferCtx, deferCancel := ctrdutil.DeferContext()
			defer deferCancel()
			if cleanupErr = container.Delete(deferCtx, containerd.WithSnapshotCleanup); cleanupErr != nil {
				log.G(ctx).WithError(cleanupErr).Errorf("Failed to delete containerd container %q", id)
			}
		}
	}()

	// Send CONTAINER_CREATED event with both ContainerId and SandboxId equal to SandboxId.
	// Note that this has to be done after sandboxStore.Add() because we need to get
	// SandboxStatus from the store and include it in the event.
	c.cri.GenerateAndSendContainerEvent(ctx, id, id, runtime.ContainerEventType_CONTAINER_CREATED_EVENT)

	// Create sandbox container root directories.
	sandboxRootDir := c.getSandboxRootDir(id)
	if err := c.os.MkdirAll(sandboxRootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sandbox root directory %q: %w",
			sandboxRootDir, err)
	}
	defer func() {
		if retErr != nil && cleanupErr == nil {
			// Cleanup the sandbox root directory.
			if cleanupErr = c.os.RemoveAll(sandboxRootDir); cleanupErr != nil {
				log.G(ctx).WithError(cleanupErr).Errorf("Failed to remove sandbox root directory %q",
					sandboxRootDir)
			}
		}
	}()

	volatileSandboxRootDir := c.getVolatileSandboxRootDir(id)
	if err := c.os.MkdirAll(volatileSandboxRootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create volatile sandbox root directory %q: %w",
			volatileSandboxRootDir, err)
	}
	defer func() {
		if retErr != nil && cleanupErr == nil {
			// Cleanup the volatile sandbox root directory.
			if cleanupErr = c.os.RemoveAll(volatileSandboxRootDir); cleanupErr != nil {
				log.G(ctx).WithError(cleanupErr).Errorf("Failed to remove volatile sandbox root directory %q",
					volatileSandboxRootDir)
			}
		}
	}()

	// Setup files required for the sandbox.
	if err = c.setupSandboxFiles(id, config); err != nil {
		return nil, fmt.Errorf("failed to setup sandbox files: %w", err)
	}
	defer func() {
		if retErr != nil && cleanupErr == nil {
			if cleanupErr = c.cleanupSandboxFiles(id, config); cleanupErr != nil {
				log.G(ctx).WithError(cleanupErr).Errorf("Failed to cleanup sandbox files in %q",
					sandboxRootDir)
			}
		}
	}()

	// Update sandbox created timestamp.
	info, err := container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox container info: %w", err)
	}

	// Create sandbox task in containerd.
	log.G(ctx).Tracef("Create sandbox container (id=%q, name=%q).", id, metadata.Name)

	taskOpts := c.taskOpts(ociRuntime.Type)
	if ociRuntime.Path != "" {
		taskOpts = append(taskOpts, containerd.WithRuntimePath(ociRuntime.Path))
	}

	// We don't need stdio for sandbox container.
	task, err := container.NewTask(ctx, containerdio.NullIO, taskOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd task: %w", err)
	}
	defer func() {
		if retErr != nil && cleanupErr == nil {
			deferCtx, deferCancel := ctrdutil.DeferContext()
			defer deferCancel()
			// Cleanup the sandbox container if an error is returned.
			if _, err := task.Delete(deferCtx, WithNRISandboxDelete(id), containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
				log.G(ctx).WithError(err).Errorf("Failed to delete sandbox container %q", id)
				cleanupErr = err
			}
		}
	}()

	// wait is a long running background request, no timeout needed.
	exitCh, err := task.Wait(ctrdutil.NamespacedContext())
	if err != nil {
		return nil, fmt.Errorf("failed to wait for sandbox container task: %w", err)
	}
	c.store.Save(id, exitCh)

	nric, err := nri.New()
	if err != nil {
		return nil, fmt.Errorf("unable to create nri client: %w", err)
	}
	if nric != nil {
		nriSB := &nri.Sandbox{
			ID:     id,
			Labels: config.Labels,
		}
		if _, err := nric.InvokeWithSandbox(ctx, task, v1.Create, nriSB); err != nil {
			return nil, fmt.Errorf("nri invoke: %w", err)
		}
	}

	if err := task.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start sandbox container task %q: %w", id, err)
	}

	// Send CONTAINER_STARTED event with ContainerId equal to SandboxId.
	c.cri.GenerateAndSendContainerEvent(ctx, id, id, runtime.ContainerEventType_CONTAINER_STARTED_EVENT)

	resp = &api.ControllerStartResponse{
		SandboxID: id,
		Pid:       task.Pid(),
		CreatedAt: protobuf.ToTimestamp(info.CreatedAt),
		Labels:    labels,
	}

	return resp, nil
}

func (c *Controller) Create(ctx context.Context, _id string) error {
	// Not used by pod-sandbox implementation as there is no need to split pause containers logic.
	return nil
}

// untrustedWorkload returns true if the sandbox contains untrusted workload.
func untrustedWorkload(config *runtime.PodSandboxConfig) bool {
	return config.GetAnnotations()[annotations.UntrustedWorkload] == "true"
}

// hostAccessingSandbox returns true if the sandbox configuration
// requires additional host access for the sandbox.
func hostAccessingSandbox(config *runtime.PodSandboxConfig) bool {
	securityContext := config.GetLinux().GetSecurityContext()

	namespaceOptions := securityContext.GetNamespaceOptions()
	if namespaceOptions.GetNetwork() == runtime.NamespaceMode_NODE ||
		namespaceOptions.GetPid() == runtime.NamespaceMode_NODE ||
		namespaceOptions.GetIpc() == runtime.NamespaceMode_NODE {
		return true
	}

	return false
}

// getSandboxRuntime returns the runtime configuration for sandbox.
// If the sandbox contains untrusted workload, runtime for untrusted workload will be returned,
// or else default runtime will be returned.
func (c *Controller) getSandboxRuntime(config *runtime.PodSandboxConfig, runtimeHandler string) (criconfig.Runtime, error) {
	if untrustedWorkload(config) {
		// If the untrusted annotation is provided, runtimeHandler MUST be empty.
		if runtimeHandler != "" && runtimeHandler != criconfig.RuntimeUntrusted {
			return criconfig.Runtime{}, errors.New("untrusted workload with explicit runtime handler is not allowed")
		}

		//  If the untrusted workload is requesting access to the host/node, this request will fail.
		//
		//  Note: If the workload is marked untrusted but requests privileged, this can be granted, as the
		// runtime may support this.  For example, in a virtual-machine isolated runtime, privileged
		// is a supported option, granting the workload to access the entire guest VM instead of host.
		// TODO(windows): Deprecate this so that we don't need to handle it for windows.
		if hostAccessingSandbox(config) {
			return criconfig.Runtime{}, errors.New("untrusted workload with host access is not allowed")
		}

		runtimeHandler = criconfig.RuntimeUntrusted
	}

	if runtimeHandler == "" {
		runtimeHandler = c.config.ContainerdConfig.DefaultRuntimeName
	}

	handler, ok := c.config.ContainerdConfig.Runtimes[runtimeHandler]
	if !ok {
		return criconfig.Runtime{}, fmt.Errorf("no runtime for %q is configured", runtimeHandler)
	}
	return handler, nil
}
