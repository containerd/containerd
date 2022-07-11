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
	"errors"
	"fmt"

	"github.com/containerd/containerd"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/cri/annotations"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	customopts "github.com/containerd/containerd/pkg/cri/opts"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/containerd/pkg/cri/util"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/pkg/netns"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/nri"
	v1 "github.com/containerd/nri/types/v1"
	"github.com/davecgh/go-spew/spew"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *Controller) Start(ctx context.Context, sandboxID string) (_ uint32, retErr error) {
	sandboxInfo, err := c.client.SandboxStore().Get(ctx, sandboxID)
	if err != nil {
		return 0, fmt.Errorf("unable to find sandbox with id %q: %w", sandboxID, err)
	}

	name, err := sandboxInfo.GetLabel("name")
	if err != nil {
		return 0, err
	}

	var (
		id              = sandboxInfo.ID
		config          runtime.PodSandboxConfig
		networkMetadata sandboxstore.PodNetworkMetadata
	)

	if err := sandboxInfo.GetExtension("config", &config); err != nil {
		return 0, err
	}

	if err := sandboxInfo.GetExtension("network", &networkMetadata); err != nil {
		return 0, err
	}

	// Create initial internal sandbox object.
	sandbox := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:             id,
			Name:           name,
			Config:         &config,
			RuntimeHandler: sandboxInfo.Runtime.Name,
		},
		sandboxstore.Status{
			State: sandboxstore.StateUnknown,
		},
	)

	// Copy over network metadata for compatibility
	sandbox.NetNSPath = networkMetadata.NetNSPath
	sandbox.NetNS = netns.LoadNetNS(networkMetadata.NetNSPath)
	sandbox.IP = networkMetadata.IP
	sandbox.AdditionalIPs = networkMetadata.AdditionalIPs
	sandbox.CNIResult = networkMetadata.CNIResult

	// Ensure sandbox container image snapshot.
	image, err := c.cri.EnsureImageExists(ctx, c.config.SandboxImage, &config)
	if err != nil {
		return 0, fmt.Errorf("failed to get sandbox image %q: %w", c.config.SandboxImage, err)
	}

	containerdImage, err := c.toContainerdImage(ctx, *image)
	if err != nil {
		return 0, fmt.Errorf("failed to get image from containerd %q: %w", image.ID, err)
	}

	ociRuntime, err := c.getSandboxRuntime(&config, sandbox.RuntimeHandler)
	if err != nil {
		return 0, fmt.Errorf("failed to get sandbox runtime: %w", err)
	}

	log.G(ctx).WithField("podsandboxid", id).Debugf("use OCI runtime %+v", ociRuntime)

	// Create sandbox container.
	// NOTE: sandboxContainerSpec SHOULD NOT have side
	// effect, e.g. accessing/creating files, so that we can test
	// it safely.
	spec, err := c.sandboxContainerSpec(id, &config, &image.ImageSpec.Config, sandbox.NetNSPath, ociRuntime.PodAnnotations)
	if err != nil {
		return 0, fmt.Errorf("failed to generate sandbox container spec: %w", err)
	}

	log.G(ctx).WithField("podsandboxid", id).Debugf("sandbox container spec: %#+v", spew.NewFormatter(spec))
	sandbox.ProcessLabel = spec.Process.SelinuxLabel
	defer func() {
		if retErr != nil {
			selinux.ReleaseLabel(sandbox.ProcessLabel)
		}
	}()

	// handle any KVM based runtime
	if err := modifyProcessLabel(ociRuntime.Type, spec); err != nil {
		return 0, err
	}

	if config.GetLinux().GetSecurityContext().GetPrivileged() {
		// If privileged don't set selinux label, but we still record the MCS label so that
		// the unused label can be freed later.
		spec.Process.SelinuxLabel = ""
	}

	// Generate spec options that will be applied to the spec later.
	specOpts, err := c.sandboxContainerSpecOpts(&config, &image.ImageSpec.Config)
	if err != nil {
		return 0, fmt.Errorf("failed to generate sandbox container spec options: %w", err)
	}

	sandboxLabels := buildLabels(config.Labels, image.ImageSpec.Config.Labels, containerKindSandbox)

	runtimeOpts, err := generateRuntimeOptions(ociRuntime, c.config)
	if err != nil {
		return 0, fmt.Errorf("failed to generate runtime options: %w", err)
	}

	snapshotterOpt := snapshots.WithLabels(snapshots.FilterInheritedLabels(config.Annotations))
	opts := []containerd.NewContainerOpts{
		containerd.WithSnapshotter(c.runtimeSnapshotter(ctx, ociRuntime)),
		customopts.WithNewSnapshot(id, containerdImage, snapshotterOpt),
		containerd.WithSpec(spec, specOpts...),
		containerd.WithContainerLabels(sandboxLabels),
		containerd.WithContainerExtension(sandboxMetadataExtension, &sandbox.Metadata),
		containerd.WithRuntime(ociRuntime.Type, runtimeOpts),
	}

	container, err := c.client.NewContainer(ctx, id, opts...)
	if err != nil {
		return 0, fmt.Errorf("failed to create containerd container: %w", err)
	}
	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := ctrdutil.DeferContext()
			defer deferCancel()
			if err := container.Delete(deferCtx, containerd.WithSnapshotCleanup); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to delete containerd container %q", id)
			}
		}
	}()

	// Create sandbox container root directories.
	sandboxRootDir := c.getSandboxRootDir(id)
	if err := c.os.MkdirAll(sandboxRootDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create sandbox root directory %q: %w",
			sandboxRootDir, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the sandbox root directory.
			if err := c.os.RemoveAll(sandboxRootDir); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to remove sandbox root directory %q",
					sandboxRootDir)
			}
		}
	}()
	volatileSandboxRootDir := c.getVolatileSandboxRootDir(id)
	if err := c.os.MkdirAll(volatileSandboxRootDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create volatile sandbox root directory %q: %w",
			volatileSandboxRootDir, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the volatile sandbox root directory.
			if err := c.os.RemoveAll(volatileSandboxRootDir); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to remove volatile sandbox root directory %q",
					volatileSandboxRootDir)
			}
		}
	}()

	// Setup files required for the sandbox.
	if err = c.setupSandboxFiles(id, &config); err != nil {
		return 0, fmt.Errorf("failed to setup sandbox files: %w", err)
	}
	defer func() {
		if retErr != nil {
			if err = c.cleanupSandboxFiles(id, &config); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to cleanup sandbox files in %q",
					sandboxRootDir)
			}
		}
	}()

	// Update sandbox created timestamp.
	info, err := container.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get sandbox container info: %w", err)
	}

	// Create sandbox task in containerd.
	log.G(ctx).Tracef("Create sandbox container (id=%q, name=%q).",
		id, name)

	taskOpts := c.taskOpts(ociRuntime.Type)
	if ociRuntime.Path != "" {
		taskOpts = append(taskOpts, containerd.WithRuntimePath(ociRuntime.Path))
	}

	// We don't need stdio for sandbox container.
	task, err := container.NewTask(ctx, containerdio.NullIO, taskOpts...)
	if err != nil {
		return 0, fmt.Errorf("failed to create containerd task: %w", err)
	}
	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := ctrdutil.DeferContext()
			defer deferCancel()
			// Cleanup the sandbox container if an error is returned.
			if _, err := task.Delete(deferCtx, WithNRISandboxDelete(id), containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
				log.G(ctx).WithError(err).Errorf("Failed to delete sandbox container %q", id)
			}
		}
	}()

	// wait is a long running background request, no timeout needed.
	exitCh, err := task.Wait(ctrdutil.NamespacedContext())
	if err != nil {
		return 0, fmt.Errorf("failed to wait for sandbox container task: %w", err)
	}

	nric, err := nri.New()
	if err != nil {
		return 0, fmt.Errorf("unable to create nri client: %w", err)
	}
	if nric != nil {
		nriSB := &nri.Sandbox{
			ID:     id,
			Labels: config.Labels,
		}
		if _, err := nric.InvokeWithSandbox(ctx, task, v1.Create, nriSB); err != nil {
			return 0, fmt.Errorf("nri invoke: %w", err)
		}
	}

	if err := task.Start(ctx); err != nil {
		return 0, fmt.Errorf("failed to start sandbox container task %q: %w", id, err)
	}

	if err := sandbox.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		// Set the pod sandbox as ready after successfully start sandbox container.
		status.Pid = task.Pid()
		status.State = sandboxstore.StateReady
		status.CreatedAt = info.CreatedAt
		return status, nil
	}); err != nil {
		return 0, fmt.Errorf("failed to update sandbox status: %w", err)
	}

	// Add sandbox into sandbox store in INIT state.
	sandbox.Container = container

	if err := c.sandboxStore.Add(sandbox); err != nil {
		return 0, fmt.Errorf("failed to add sandbox %+v into store: %w", sandbox, err)
	}

	// start the monitor after adding sandbox into the store, this ensures
	// that sandbox is in the store, when event monitor receives the TaskExit event.
	//
	// TaskOOM from containerd may come before sandbox is added to store,
	// but we don't care about sandbox TaskOOM right now, so it is fine.
	c.cri.StartSandboxExitMonitor(context.Background(), id, task.Pid(), exitCh)

	return task.Pid(), nil
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

// runtimeSpec returns a default runtime spec used in cri-containerd.
func (c *Controller) runtimeSpec(id string, baseSpecFile string, opts ...oci.SpecOpts) (*runtimespec.Spec, error) {
	// GenerateSpec needs namespace.
	ctx := ctrdutil.NamespacedContext()
	container := &containers.Container{ID: id}

	if baseSpecFile != "" {
		baseSpec, ok := c.baseOCISpecs[baseSpecFile]
		if !ok {
			return nil, fmt.Errorf("can't find base OCI spec %q", baseSpecFile)
		}

		spec := oci.Spec{}
		if err := util.DeepCopy(&spec, &baseSpec); err != nil {
			return nil, fmt.Errorf("failed to clone OCI spec: %w", err)
		}

		// Fix up cgroups path
		applyOpts := append([]oci.SpecOpts{oci.WithNamespacedCgroup()}, opts...)

		if err := oci.ApplyOpts(ctx, nil, container, &spec, applyOpts...); err != nil {
			return nil, fmt.Errorf("failed to apply OCI options: %w", err)
		}

		return &spec, nil
	}

	spec, err := oci.GenerateSpec(ctx, nil, container, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to generate spec: %w", err)
	}

	return spec, nil
}

// Overrides the default snapshotter if Snapshotter is set for this runtime.
// See https://github.com/containerd/containerd/issues/6657
func (c *Controller) runtimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string {
	if ociRuntime.Snapshotter == "" {
		return c.config.ContainerdConfig.Snapshotter
	}

	log.G(ctx).Debugf("Set snapshotter for runtime %s to %s", ociRuntime.Type, ociRuntime.Snapshotter)
	return ociRuntime.Snapshotter
}
