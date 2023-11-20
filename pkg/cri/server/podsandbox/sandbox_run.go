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

	"github.com/containerd/log"
	"github.com/containerd/nri"
	v1 "github.com/containerd/nri/types/v1"
	"github.com/containerd/typeurl/v2"
	"github.com/davecgh/go-spew/spew"
	"github.com/opencontainers/selinux/go-selinux"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerdio "github.com/containerd/containerd/v2/cio"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/errdefs"
	crilabels "github.com/containerd/containerd/v2/pkg/cri/labels"
	customopts "github.com/containerd/containerd/v2/pkg/cri/opts"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/sandbox"
	"github.com/containerd/containerd/v2/snapshots"
)

func init() {
	typeurl.Register(&sandboxstore.Metadata{},
		"github.com/containerd/cri/pkg/store/sandbox", "Metadata")
}

type CleanupErr struct {
	error
}

// Start creates resources required for the sandbox and starts the sandbox.  If an error occurs, Start attempts to tear
// down the created resources.  If an error occurs while tearing down resources, a zero-valued response is returned
// alongside the error.  If the teardown was successful, a nil response is returned with the error.
// TODO(samuelkarp) Determine whether this error indication is reasonable to retain once controller.Delete is implemented.
func (c *Controller) Start(ctx context.Context, id string) (cin sandbox.ControllerInstance, retErr error) {
	var cleanupErr error
	defer func() {
		if retErr != nil && cleanupErr != nil {
			log.G(ctx).WithField("id", id).WithError(cleanupErr).Errorf("failed to fully teardown sandbox resources after earlier error: %s", retErr)
			retErr = errors.Join(retErr, CleanupErr{cleanupErr})
		}
	}()

	sandboxInfo, err := c.client.SandboxStore().Get(ctx, id)
	if err != nil {
		return cin, fmt.Errorf("unable to find sandbox with id %q: %w", id, err)
	}

	var metadata sandboxstore.Metadata
	if err := sandboxInfo.GetExtension(MetadataKey, &metadata); err != nil {
		return cin, fmt.Errorf("failed to get sandbox %q metadata: %w", id, err)
	}

	var (
		config = metadata.Config
		labels = map[string]string{}
	)

	// Ensure sandbox container image snapshot.
	image, err := c.ensureImageExists(ctx, c.config.SandboxImage, config)
	if err != nil {
		return cin, fmt.Errorf("failed to get sandbox image %q: %w", c.config.SandboxImage, err)
	}

	containerdImage, err := c.toContainerdImage(ctx, *image)
	if err != nil {
		return cin, fmt.Errorf("failed to get image from containerd %q: %w", image.ID, err)
	}

	ociRuntime, err := c.config.GetSandboxRuntime(config, metadata.RuntimeHandler)
	if err != nil {
		return cin, fmt.Errorf("failed to get sandbox runtime: %w", err)
	}
	log.G(ctx).WithField("podsandboxid", id).Debugf("use OCI runtime %+v", ociRuntime)

	labels["oci_runtime_type"] = ociRuntime.Type

	// Create sandbox container.
	// NOTE: sandboxContainerSpec SHOULD NOT have side
	// effect, e.g. accessing/creating files, so that we can test
	// it safely.
	spec, err := c.sandboxContainerSpec(id, config, &image.ImageSpec.Config, metadata.NetNSPath, ociRuntime.PodAnnotations)
	if err != nil {
		return cin, fmt.Errorf("failed to generate sandbox container spec: %w", err)
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
		return cin, err
	}

	if config.GetLinux().GetSecurityContext().GetPrivileged() {
		// If privileged don't set selinux label, but we still record the MCS label so that
		// the unused label can be freed later.
		spec.Process.SelinuxLabel = ""
	}

	// Generate spec options that will be applied to the spec later.
	specOpts, err := c.sandboxContainerSpecOpts(config, &image.ImageSpec.Config)
	if err != nil {
		return cin, fmt.Errorf("failed to generate sandbox container spec options: %w", err)
	}

	sandboxLabels := buildLabels(config.Labels, image.ImageSpec.Config.Labels, crilabels.ContainerKindSandbox)

	snapshotterOpt := []snapshots.Opt{snapshots.WithLabels(snapshots.FilterInheritedLabels(config.Annotations))}
	extraSOpts, err := sandboxSnapshotterOpts(config)
	if err != nil {
		return cin, err
	}
	snapshotterOpt = append(snapshotterOpt, extraSOpts...)

	opts := []containerd.NewContainerOpts{
		containerd.WithSnapshotter(c.runtimeSnapshotter(ctx, ociRuntime)),
		customopts.WithNewSnapshot(id, containerdImage, snapshotterOpt...),
		containerd.WithSpec(spec, specOpts...),
		containerd.WithContainerLabels(sandboxLabels),
		containerd.WithContainerExtension(crilabels.SandboxMetadataExtension, &metadata),
		containerd.WithRuntime(ociRuntime.Type, sandboxInfo.Runtime.Options),
	}

	container, err := c.client.NewContainer(ctx, id, opts...)
	if err != nil {
		return cin, fmt.Errorf("failed to create containerd container: %w", err)
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

	// Create sandbox container root directories.
	sandboxRootDir := c.getSandboxRootDir(id)
	if err := c.os.MkdirAll(sandboxRootDir, 0755); err != nil {
		return cin, fmt.Errorf("failed to create sandbox root directory %q: %w",
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
		return cin, fmt.Errorf("failed to create volatile sandbox root directory %q: %w",
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
		return cin, fmt.Errorf("failed to setup sandbox files: %w", err)
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
		return cin, fmt.Errorf("failed to get sandbox container info: %w", err)
	}

	// Create sandbox task in containerd.
	log.G(ctx).Tracef("Create sandbox container (id=%q, name=%q).", id, metadata.Name)

	var taskOpts []containerd.NewTaskOpts
	if ociRuntime.Path != "" {
		taskOpts = append(taskOpts, containerd.WithRuntimePath(ociRuntime.Path))
	}

	// We don't need stdio for sandbox container.
	task, err := container.NewTask(ctx, containerdio.NullIO, taskOpts...)
	if err != nil {
		return cin, fmt.Errorf("failed to create containerd task: %w", err)
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
		return cin, fmt.Errorf("failed to wait for sandbox container task: %w", err)
	}
	c.store.Save(id, exitCh)

	nric, err := nri.New()
	if err != nil {
		return cin, fmt.Errorf("unable to create nri client: %w", err)
	}
	if nric != nil {
		nriSB := &nri.Sandbox{
			ID:     id,
			Labels: config.Labels,
		}
		if _, err := nric.InvokeWithSandbox(ctx, task, v1.Create, nriSB); err != nil {
			return cin, fmt.Errorf("nri invoke: %w", err)
		}
	}

	if err := task.Start(ctx); err != nil {
		return cin, fmt.Errorf("failed to start sandbox container task %q: %w", id, err)
	}

	cin.SandboxID = id
	cin.Pid = task.Pid()
	cin.CreatedAt = info.CreatedAt
	cin.Labels = labels

	return
}

func (c *Controller) Create(ctx context.Context, _info sandbox.Sandbox, _ ...sandbox.CreateOpt) error {
	// Not used by pod-sandbox implementation as there is no need to split pause containers logic.
	return nil
}

func (c *Controller) ensureImageExists(ctx context.Context, ref string, config *runtime.PodSandboxConfig) (*imagestore.Image, error) {
	image, err := c.imageService.LocalResolve(ref)
	if err != nil && !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get image %q: %w", ref, err)
	}
	if err == nil {
		return &image, nil
	}
	// Pull image to ensure the image exists
	resp, err := c.imageService.PullImage(ctx, &runtime.PullImageRequest{Image: &runtime.ImageSpec{Image: ref}, SandboxConfig: config})
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %w", ref, err)
	}
	imageID := resp.GetImageRef()
	newImage, err := c.imageService.GetImage(imageID)
	if err != nil {
		// It's still possible that someone removed the image right after it is pulled.
		return nil, fmt.Errorf("failed to get image %q after pulling: %w", imageID, err)
	}
	return &newImage, nil
}
