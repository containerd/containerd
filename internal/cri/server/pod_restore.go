/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0
*/

package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/containerd/typeurl/v2"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/leases"
	runtimev2 "github.com/containerd/containerd/v2/core/runtime/v2"
	sb "github.com/containerd/containerd/v2/core/sandbox"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/server/podsandbox"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/netns"
)

const restoreStateLabel = "io.cri-containerd.restore-state"

const terminationMessagePathAnnotation = "io.kubernetes.container.terminationMessagePath"

const (
	restoreStateRestoring       = "restoring"
	restoreStateRestoredCreated = "restored-created"
)

type restoreContainerPlan struct {
	name      string
	id        string
	fullName  string
	config    *runtime.ContainerConfig
	prepared  *runtimev2.PreparedRestoredTask
	adopted   bool
	container containerstore.Container
	image     imagestore.Image
	ctrdImage containerd.Image
}

// RestorePod restores one complete sandbox. The sandbox runtime owns process
// state; containerd only stages host resources and adopts the resulting tasks.
func (c *criService) RestorePod(ctx context.Context, request *runtime.RestorePodRequest) (_ *runtime.RestorePodResponse, retErr error) {
	config, plans, ociRuntime, err := c.validateRestorePodRequest(request)
	if err != nil {
		return nil, err
	}
	restoreService, ok := c.sandboxService.(checkpointRestoreSandboxService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "sandbox service does not support restore")
	}
	controller, err := c.sandboxService.SandboxController(ociRuntime.Sandboxer)
	if err != nil {
		return nil, toCRIError(err)
	}
	if err := validateRestoreSupport(controller, ociRuntime.Sandboxer, c.nri.IsDisabled()); err != nil {
		return nil, err
	}
	if c.restoredTaskManager == nil {
		return nil, status.Error(codes.Unimplemented, "tasks service does not support restored task adoption")
	}
	if err := c.preflightRestoreContainers(ctx, plans); err != nil {
		return nil, err
	}
	platform, err := restorePodPlatform(plans)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	sandboxID := util.GenerateID()
	unlock, err := c.lockSandboxOperation(ctx, sandboxID)
	if err != nil {
		return nil, toCRIError(err)
	}
	defer unlock()
	sandboxName := makeSandboxName(config.GetMetadata())
	for i := range plans {
		plans[i].id = util.GenerateID()
		plans[i].fullName = makeContainerName(plans[i].config.GetMetadata(), config.GetMetadata())
	}

	runtimeOpts, err := criconfig.GenerateRuntimeOptions(ociRuntime)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid runtime options: %v", err)
	}
	sandboxInfo := sb.Sandbox{ID: sandboxID, Sandboxer: ociRuntime.Sandboxer}
	sandboxInfo.Runtime.Name = ociRuntime.Type
	if runtimeOpts != nil {
		sandboxInfo.Runtime.Options, err = typeurl.MarshalAny(runtimeOpts)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "failed to marshal runtime options: %v", err)
		}
	}
	sandboxInfo.AddLabel("name", sandboxName)
	sandboxInfo.AddLabel(restoreStateLabel, restoreStateRestoring)

	restoreMetadata := &sandboxstore.RestoreMetadata{
		State: restoreStateRestoring, CheckpointPath: request.GetCheckpointPath(),
	}
	for i := range plans {
		restoreMetadata.ExpectedContainers = append(restoreMetadata.ExpectedContainers, sandboxstore.RestoreContainer{Name: plans[i].name, ID: plans[i].id})
	}
	sandbox := sandboxstore.NewSandbox(sandboxstore.Metadata{
		ID: sandboxID, Name: sandboxName, Config: config, RuntimeHandler: request.GetRuntimeHandler(),
		Restore: restoreMetadata,
	}, sandboxstore.Status{State: sandboxstore.StateUnknown, CreatedAt: time.Now().UTC()})
	sandbox.Sandboxer = ociRuntime.Sandboxer
	if err := sandboxInfo.AddExtension(podsandbox.MetadataKey, &sandbox.Metadata); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to persist restore metadata: %v", err)
	}

	// The durable Restoring marker is the first externally visible side effect.
	if _, err := c.client.SandboxStore().Create(ctx, sandboxInfo); err != nil {
		return nil, toCRIError(fmt.Errorf("failed to create restoring record: %w", err))
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		cc, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancelCleanup()
		cleanupFailed := false
		var cleanupErr error
		recordCleanup := func(resource string, err error) {
			if err == nil || errdefs.IsNotFound(err) {
				return
			}
			cleanupFailed = true
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("%s: %w", resource, err))
			log.G(cc).WithError(err).Errorf("failed to clean restored %s for sandbox %q", resource, sandboxID)
		}
		for i := len(plans) - 1; i >= 0; i-- {
			if plans[i].prepared != nil || plans[i].adopted {
				recordCleanup("task resources", c.restoredTaskManager.CleanupRestoredTask(cc, plans[i].id))
			}
		}
		recordCleanup("sandbox runtime", c.sandboxService.ShutdownSandbox(cc, sandbox.Sandboxer, sandboxID))
		for i := len(plans) - 1; i >= 0; i-- {
			if plans[i].container.Container != nil {
				c.containerStore.Delete(plans[i].id)
				if spec, err := plans[i].container.Container.Spec(cc); err == nil {
					c.nri.UndoCreateContainer(cc, &sandbox, plans[i].id, spec)
				}
				if plans[i].container.IO != nil {
					recordCleanup("container IO", plans[i].container.IO.Close())
				}
				recordCleanup("container metadata", plans[i].container.Container.Delete(cc))
				recordCleanup("container status", plans[i].container.Delete())
				recordCleanup("container root", ensureRemoveAll(cc, c.getContainerRootDir(plans[i].id)))
				recordCleanup("container state", ensureRemoveAll(cc, c.getVolatileContainerRootDir(plans[i].id)))
			}
		}
		recordCleanup("image mounts", c.cleanupImageMountsWithSnapshotter(
			cc, sandboxID, c.RuntimeSnapshotter(cc, ociRuntime),
		))
		if sandbox.NetNS != nil && !hostNetwork(sandbox.Config) {
			recordCleanup("pod network", c.teardownPodNetwork(cc, sandbox))
		}
		if sandbox.NetNS != nil {
			recordCleanup("network namespace", sandbox.NetNS.Remove())
		}
		recordCleanup("lease", c.client.LeasesService().Delete(cc, leases.Lease{ID: sandboxID}))
		// Delete the marker last. If any earlier cleanup remains, startup recovery
		// can retry using this record. Names and IDs remain reserved until the
		// durable record itself has gone away.
		if !cleanupFailed {
			err := c.client.SandboxStore().Delete(cc, sandboxID)
			recordCleanup("restoring record", err)
			if err == nil || errdefs.IsNotFound(err) {
				c.sandboxStore.Delete(sandboxID)
				c.sandboxNameIndex.ReleaseByKey(sandboxID)
				for i := range plans {
					c.containerNameIndex.ReleaseByKey(plans[i].id)
				}
			}
		}
		if cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("restore rollback incomplete: %w", cleanupErr))
		}
	}()

	if err := c.sandboxNameIndex.Reserve(sandboxName, sandboxID); err != nil {
		return nil, toCRIError(fmt.Errorf("failed to reserve sandbox name: %w", err))
	}
	if _, err := c.client.LeasesService().Create(ctx, leases.WithID(sandboxID)); err != nil {
		return nil, toCRIError(fmt.Errorf("failed to create restore lease: %w", err))
	}
	if err := c.setupRestorePodNetwork(ctx, &sandbox, &sandboxInfo); err != nil {
		return nil, toCRIError(err)
	}
	for i := range plans {
		if err := c.containerNameIndex.Reserve(plans[i].fullName, plans[i].id); err != nil {
			return nil, toCRIError(fmt.Errorf("failed to reserve container name %q: %w", plans[i].name, err))
		}
		cntr, err := c.stageRestoredContainer(ctx, &sandbox, &plans[i], &platform, &ociRuntime)
		if err != nil {
			return nil, toCRIError(err)
		}
		plans[i].container = cntr
		taskRequest, err := containerd.NewRestoredTaskRequest(ctx, cntr.Container, cntr.IO.Config())
		if err != nil {
			return nil, toCRIError(fmt.Errorf("failed to build task request for %q: %w", plans[i].name, err))
		}
		plans[i].prepared, err = c.restoredTaskManager.PrepareRestoredTask(ctx, taskRequest)
		if err != nil {
			return nil, toCRIError(fmt.Errorf("failed to prepare restored task %q: %w", plans[i].name, err))
		}
	}

	packedConfig, err := typeurl.MarshalAny(config)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal sandbox config: %v", err)
	}
	restoreTasks := make([]sb.RestoreTask, 0, len(plans))
	for i := range plans {
		prepared := plans[i].prepared.Create
		restoreTasks = append(restoreTasks, sb.RestoreTask{
			CheckpointKey: plans[i].name,
			TaskID:        plans[i].id,
			Bundle:        prepared.Bundle,
			Terminal:      prepared.Terminal,
			Stdin:         prepared.Stdin,
			Stdout:        prepared.Stdout,
			Stderr:        prepared.Stderr,
			Options:       prepared.Options,
		})
	}
	result, err := restoreService.RestoreSandbox(ctx, sandbox.Sandboxer, sandboxInfo, sb.RestoreOptions{
		CheckpointPath: request.GetCheckpointPath(), SandboxOptions: packedConfig,
		NetNSPath: sandbox.NetNSPath, Options: cloneStringMap(request.GetOptions()), Tasks: restoreTasks,
	})
	if err != nil {
		return nil, toCRIError(err)
	}
	if err := validateRestoreResult(plans, result); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	for i := range plans {
		if _, err := c.restoredTaskManager.AdoptRestoredTask(ctx, plans[i].prepared, result.Controller.Address, result.Controller.Version); err != nil {
			return nil, toCRIError(fmt.Errorf("failed to adopt restored task %q: %w", plans[i].name, err))
		}
		plans[i].adopted = true
		labelOpt := containerd.UpdateContainerOpts(containerd.WithAdditionalContainerLabels(map[string]string{restoredTaskSourceLabel: restoredTaskSourceValue}))
		if err := plans[i].container.Container.Update(ctx, labelOpt); err != nil {
			return nil, toCRIError(fmt.Errorf("failed to mark restored task: %w", err))
		}
	}

	sandbox.Endpoint = sandboxstore.Endpoint{Address: result.Controller.Address, Version: result.Controller.Version}
	// Persist runtime results while retaining the Restoring marker. A crash from
	// here until final commit must still select cleanup-only recovery.
	for key, value := range result.Controller.Labels {
		sandboxInfo.AddLabel(key, value)
	}
	sandboxInfo.Spec = result.Controller.Spec
	if _, err := c.client.SandboxStore().Update(ctx, sandboxInfo, "labels", "spec", "extensions"); err != nil {
		return nil, toCRIError(fmt.Errorf("failed to persist restored sandbox state: %w", err))
	}
	if err := sandbox.Status.Update(func(s sandboxstore.Status) (sandboxstore.Status, error) {
		s.Pid, s.CreatedAt = result.Controller.Pid, result.Controller.CreatedAt
		return s, nil
	}); err != nil {
		return nil, toCRIError(err)
	}
	if err := sandbox.Status.Update(func(s sandboxstore.Status) (sandboxstore.Status, error) {
		s.State = sandboxstore.StateReady
		return s, nil
	}); err != nil {
		return nil, toCRIError(err)
	}
	exitCh, err := c.sandboxService.WaitSandbox(util.NamespacedContext(), sandbox.Sandboxer, sandboxID)
	if err != nil {
		return nil, toCRIError(fmt.Errorf("failed to wait restored sandbox: %w", err))
	}

	// Durably commit RestoredCreated before exposing any restored object through
	// CRI stores. A crash during publication is therefore unambiguously recovered
	// as an incomplete restore rather than exposing objects without a marker.
	sandbox.Metadata.Restore.State = restoreStateRestoredCreated
	if err := sandboxInfo.AddExtension(podsandbox.MetadataKey, &sandbox.Metadata); err != nil {
		return nil, toCRIError(fmt.Errorf("failed to commit restore metadata: %w", err))
	}
	sandboxInfo.AddLabel(restoreStateLabel, restoreStateRestoredCreated)
	if _, err := c.client.SandboxStore().Update(ctx, sandboxInfo, "labels", "extensions"); err != nil {
		return nil, toCRIError(fmt.Errorf("failed to commit restored sandbox: %w", err))
	}

	// Publish only after every task has been restored, validated, adopted and the
	// durable intermediate commit is visible.
	if err := c.sandboxStore.Add(sandbox); err != nil {
		return nil, toCRIError(fmt.Errorf("failed to publish restored sandbox: %w", err))
	}
	// Start the monitor only after the sandbox is resolvable in sandboxStore.
	// Otherwise an early shim exit is consumed as NotFound and permanently lost.
	c.startSandboxExitMonitor(context.Background(), sandboxID, exitCh)
	for i := range plans {
		if err := c.containerStore.Add(plans[i].container); err != nil {
			return nil, toCRIError(fmt.Errorf("failed to publish restored container %q: %w", plans[i].name, err))
		}
	}
	if sandbox.Status.Get().State != sandboxstore.StateReady {
		return nil, status.Errorf(codes.Internal, "restored sandbox %q exited during publication", sandboxID)
	}
	// Publication is complete: clear the durable restore transaction before
	// returning success. A restart after the RPC commit must recover this as an
	// ordinary CREATED pod, while any crash before this update remains cleanup-only.
	if err := c.clearCompletedRestoreRecord(ctx, sandboxInfo, &sandbox.Metadata); err != nil {
		return nil, toCRIError(err)
	}
	committed = true
	c.generateAndSendContainerEvent(ctx, sandboxID, sandboxID, runtime.ContainerEventType_CONTAINER_CREATED_EVENT)
	c.generateAndSendContainerEvent(ctx, sandboxID, sandboxID, runtime.ContainerEventType_CONTAINER_STARTED_EVENT)
	for i := range plans {
		c.generateAndSendContainerEvent(ctx, plans[i].id, sandboxID, runtime.ContainerEventType_CONTAINER_CREATED_EVENT)
	}
	response := &runtime.RestorePodResponse{PodSandboxId: sandboxID}
	for i := range plans {
		response.RestoredContainers = append(response.RestoredContainers, &runtime.RestoredContainer{Name: plans[i].name, ContainerId: plans[i].id})
	}
	return response, nil
}

func validateRestoreSupport(controller sb.Controller, sandboxer string, nriDisabled bool) error {
	if _, ok := controller.(sb.CheckpointRestoreController); !ok {
		return status.Errorf(codes.Unimplemented, "sandbox controller %q does not support restore", sandboxer)
	}
	// FIXME: The current Sandbox API restores the sandbox and its tasks in one call,
	// so it cannot preserve NRI's RunPodSandbox-before-CreateContainer ordering.
	if !nriDisabled {
		return status.Error(codes.Unimplemented, "pod restore with NRI enabled requires two-phase runtime restore to preserve NRI sandbox/container ordering")
	}
	return nil
}

func (c *criService) clearCompletedRestoreRecord(ctx context.Context, sbx sb.Sandbox, metadata *sandboxstore.Metadata) error {
	metadata.Restore = nil
	if err := sbx.AddExtension(podsandbox.MetadataKey, metadata); err != nil {
		return err
	}
	delete(sbx.Labels, restoreStateLabel)
	if _, err := c.client.SandboxStore().Update(ctx, sbx, "labels", "extensions"); err != nil {
		return fmt.Errorf("clear completed restore record: %w", err)
	}
	return nil
}

func (c *criService) validateRestorePodRequest(request *runtime.RestorePodRequest) (*runtime.PodSandboxConfig, []restoreContainerPlan, criconfig.Runtime, error) {
	if request == nil {
		return nil, nil, criconfig.Runtime{}, status.Error(codes.InvalidArgument, "restore request is required")
	}
	path := request.GetCheckpointPath()
	if path == "" || !filepath.IsAbs(path) {
		return nil, nil, criconfig.Runtime{}, status.Error(codes.InvalidArgument, "checkpoint path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, criconfig.Runtime{}, status.Errorf(codes.NotFound, "checkpoint path %q does not exist", path)
		}
		return nil, nil, criconfig.Runtime{}, status.Errorf(codes.InvalidArgument, "checkpoint path is not readable: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, nil, criconfig.Runtime{}, status.Error(codes.InvalidArgument, "checkpoint path must be a non-symlink directory")
	}
	if _, err := os.ReadDir(path); err != nil {
		return nil, nil, criconfig.Runtime{}, status.Errorf(codes.InvalidArgument, "checkpoint path is not readable: %v", err)
	}
	if request.GetConfig() == nil || request.GetConfig().GetMetadata() == nil {
		return nil, nil, criconfig.Runtime{}, status.Error(codes.InvalidArgument, "pod sandbox config and metadata are required")
	}
	if userns := request.GetConfig().GetLinux().GetSecurityContext().GetNamespaceOptions().GetUsernsOptions(); userns != nil && userns.GetMode() == runtime.NamespaceMode_POD {
		return nil, nil, criconfig.Runtime{}, status.Error(codes.InvalidArgument, "pod user namespaces are unsupported by pod restore MVP")
	}
	if len(request.GetContainerConfigs()) == 0 {
		return nil, nil, criconfig.Runtime{}, status.Error(codes.InvalidArgument, "at least one container config is required")
	}
	config := proto.Clone(request.GetConfig()).(*runtime.PodSandboxConfig)
	names := map[string]struct{}{}
	plans := make([]restoreContainerPlan, 0, len(request.GetContainerConfigs()))
	for _, original := range request.GetContainerConfigs() {
		if original == nil || original.GetMetadata() == nil || original.GetMetadata().GetName() == "" {
			return nil, nil, criconfig.Runtime{}, status.Error(codes.InvalidArgument, "each container config must have a name")
		}
		name := original.GetMetadata().GetName()
		if _, found := names[name]; found {
			return nil, nil, criconfig.Runtime{}, status.Errorf(codes.InvalidArgument, "duplicate container name %q", name)
		}
		names[name] = struct{}{}
		cfg := proto.Clone(original).(*runtime.ContainerConfig)
		stripUntrustedRestoreLabels(cfg.Labels)
		if cfg.GetImage().GetImage() == "" {
			return nil, nil, criconfig.Runtime{}, status.Errorf(codes.InvalidArgument, "container %q image is required", name)
		}
		if _, err := criSignalToOCIStopSignal(cfg.GetStopSignal()); err != nil {
			return nil, nil, criconfig.Runtime{}, status.Errorf(codes.InvalidArgument, "container %q has invalid stop signal: %v", name, err)
		}
		if err := validatePodCheckpointRestoreContainerConfig(cfg); err != nil {
			return nil, nil, criconfig.Runtime{}, status.Errorf(codes.InvalidArgument, "container %q: %v", name, err)
		}
		plans = append(plans, restoreContainerPlan{name: name, config: cfg})
	}
	ociRuntime, err := c.config.GetSandboxRuntime(config, request.GetRuntimeHandler())
	if err != nil {
		return nil, nil, criconfig.Runtime{}, status.Errorf(codes.InvalidArgument, "unknown runtime handler %q: %v", request.GetRuntimeHandler(), err)
	}
	return config, plans, ociRuntime, nil
}

func validatePodCheckpointRestoreContainerConfig(config *runtime.ContainerConfig) error {
	hostsMounts := 0
	terminationMounts := 0
	terminationPath := config.GetAnnotations()[terminationMessagePathAnnotation]
	if terminationPath != "" && (!filepath.IsAbs(terminationPath) || terminationPath == "/etc/hosts") {
		return fmt.Errorf("%s must be an absolute path distinct from /etc/hosts", terminationMessagePathAnnotation)
	}
	for _, mount := range config.GetMounts() {
		if !filepath.IsAbs(mount.GetContainerPath()) {
			return fmt.Errorf("mount destination %q must be absolute", mount.GetContainerPath())
		}
		imageMount := mount.GetImage() != nil
		if imageMount {
			if mount.GetHostPath() != "" {
				return fmt.Errorf("mount %q must not set both host_path and image", mount.GetContainerPath())
			}
			if mount.GetImage().GetImage() == "" {
				return fmt.Errorf("image mount %q requires an image reference", mount.GetContainerPath())
			}
		} else if !filepath.IsAbs(mount.GetHostPath()) {
			return fmt.Errorf("mount source %q must be absolute", mount.GetHostPath())
		}
		description := ""
		switch mount.GetContainerPath() {
		case "/etc/hosts":
			if imageMount {
				return fmt.Errorf("/etc/hosts must be a host-path mount")
			}
			hostsMounts++
			if hostsMounts > 1 {
				return fmt.Errorf("duplicate /etc/hosts mounts are unsupported by pod checkpoint/restore MVP")
			}
			description = "/etc/hosts"
		case terminationPath:
			if terminationPath == "" {
				continue
			}
			if imageMount {
				return fmt.Errorf("termination-log must be a host-path mount")
			}
			terminationMounts++
			if terminationMounts > 1 {
				return fmt.Errorf("duplicate termination-log mounts are unsupported by pod checkpoint/restore MVP")
			}
			if mount.GetReadonly() {
				return fmt.Errorf("termination-log mount must be writable")
			}
			description = "termination-log"
		default:
			continue
		}
		info, err := os.Lstat(mount.GetHostPath())
		if err != nil {
			return fmt.Errorf("failed to inspect %s source: %w", description, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return fmt.Errorf("%s source must be a regular file no larger than 1 MiB", description)
		}
	}
	if userns := config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetUsernsOptions(); userns != nil && userns.GetMode() == runtime.NamespaceMode_POD {
		return fmt.Errorf("user namespaces are unsupported by pod checkpoint/restore MVP")
	}
	return nil
}

func stripUntrustedRestoreLabels(labels map[string]string) {
	delete(labels, restoredTaskSourceLabel)
}

func (c *criService) preflightRestoreContainers(ctx context.Context, plans []restoreContainerPlan) error {
	for i := range plans {
		image, err := c.LocalResolve(plans[i].config.GetImage().GetImage())
		if err != nil {
			return toCRIError(fmt.Errorf("failed to resolve image for %q: %w", plans[i].name, err))
		}
		if err := validatePodCheckpointRestoreImage(image); err != nil {
			return status.Errorf(codes.InvalidArgument, "container %q: %v", plans[i].name, err)
		}
		ctrdImage, err := c.toContainerdImage(ctx, image)
		if err != nil {
			return toCRIError(fmt.Errorf("failed to get image for %q: %w", plans[i].name, err))
		}
		plans[i].image = image
		plans[i].ctrdImage = ctrdImage
	}
	return nil
}

func restorePodPlatform(plans []restoreContainerPlan) (imagespec.Platform, error) {
	if len(plans) == 0 {
		return imagespec.Platform{}, fmt.Errorf("restore has no containers")
	}
	platform := platforms.Normalize(plans[0].image.ImageSpec.Platform)
	if platform.OS == "" || platform.Architecture == "" {
		return imagespec.Platform{}, fmt.Errorf("container %q image has no platform", plans[0].name)
	}
	for i := 1; i < len(plans); i++ {
		candidate := platforms.Normalize(plans[i].image.ImageSpec.Platform)
		if !reflect.DeepEqual(platform, candidate) {
			return imagespec.Platform{}, fmt.Errorf("container %q image platform %q differs from %q", plans[i].name, platforms.Format(candidate), platforms.Format(platform))
		}
	}
	return platform, nil
}

func validatePodCheckpointRestoreImage(image imagestore.Image) error {
	if len(image.ImageSpec.Config.Volumes) != 0 {
		return fmt.Errorf("image-defined volumes are unsupported by pod checkpoint/restore")
	}
	return nil
}

func (c *criService) stageRestoredContainer(ctx context.Context, sandbox *sandboxstore.Sandbox, plan *restoreContainerPlan, platform *imagespec.Platform, ociRuntime *criconfig.Runtime) (containerstore.Container, error) {
	meta := containerstore.Metadata{ID: plan.id, Name: plan.fullName, SandboxID: sandbox.ID, Config: plan.config}
	var staged containerstore.Container
	// RestoreSandbox is responsible for reconstructing the checkpointed rootfs.
	// Do not create a new containerd snapshot, which would replace the
	// checkpoint-authoritative writable filesystem state.
	_, err := c.createContainer(&createContainerRequest{
		ctx:                   ctx,
		containerID:           plan.id,
		sandbox:               sandbox,
		sandboxID:             sandbox.ID,
		imageID:               plan.image.ID,
		containerConfig:       plan.config,
		imageConfig:           &plan.image.ImageSpec.Config,
		podSandboxConfig:      sandbox.Config,
		sandboxRuntimeHandler: sandbox.RuntimeHandler,
		NetNSPath:             sandbox.NetNSPath,
		containerName:         plan.name,
		containerdImage:       &plan.ctrdImage,
		meta:                  &meta,
		start:                 time.Now(),
		platform:              platform,
		deferPublish:          true,
		stagedContainer:       &staged,
		ociRuntime:            ociRuntime,
		noSnapshot:            true,
	})
	if err != nil {
		return containerstore.Container{}, fmt.Errorf("failed to stage container %q: %w", plan.name, err)
	}
	return staged, nil
}

func validateRestoreResult(plans []restoreContainerPlan, result sb.RestoreResult) error {
	if result.Controller.Address == "" || result.Controller.CreatedAt.IsZero() || result.Controller.Version < 3 {
		return fmt.Errorf("restore controller returned an invalid endpoint, version or creation time")
	}
	if len(result.Tasks) != len(plans) {
		return fmt.Errorf("restore returned %d tasks, expected %d", len(result.Tasks), len(plans))
	}
	expected := make(map[string]string, len(plans))
	for _, plan := range plans {
		expected[plan.name] = plan.id
	}
	seen := map[string]struct{}{}
	for _, restored := range result.Tasks {
		id, ok := expected[restored.CheckpointKey]
		if !ok || restored.TaskID != id {
			return fmt.Errorf("restore returned unexpected task %q/%q", restored.CheckpointKey, restored.TaskID)
		}
		if _, ok := seen[restored.CheckpointKey]; ok {
			return fmt.Errorf("restore returned duplicate task %q", restored.CheckpointKey)
		}
		seen[restored.CheckpointKey] = struct{}{}
	}
	return nil
}

func (c *criService) setupRestorePodNetwork(ctx context.Context, sandbox *sandboxstore.Sandbox, sandboxInfo *sb.Sandbox) error {
	if hostNetwork(sandbox.Config) {
		return nil
	}
	netnsMountDir := "/var/run/netns"
	if c.config.NetNSMountsUnderStateDir {
		netnsMountDir = filepath.Join(c.config.StateDir, "netns")
	}
	var err error
	sandbox.NetNS, err = netns.NewNetNS(netnsMountDir)
	if err != nil {
		return fmt.Errorf("failed to create network namespace: %w", err)
	}
	sandbox.NetNSPath = sandbox.NetNS.GetPath()
	if err := sandboxInfo.AddExtension(podsandbox.MetadataKey, &sandbox.Metadata); err != nil {
		return err
	}
	if _, err := c.client.SandboxStore().Update(ctx, *sandboxInfo, "extensions"); err != nil {
		return fmt.Errorf("failed to persist network namespace: %w", err)
	}
	if err := c.setupPodNetwork(ctx, sandbox); err != nil {
		return fmt.Errorf("failed to setup pod network: %w", err)
	}
	if err := sandboxInfo.AddExtension(podsandbox.MetadataKey, &sandbox.Metadata); err != nil {
		return err
	}
	_, err = c.client.SandboxStore().Update(ctx, *sandboxInfo, "extensions")
	return err
}
