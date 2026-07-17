//go:build linux

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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/images"
	imagearchive "github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/containerd/v2/core/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/selinux/go-selinux/label"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// podCheckpointState holds the in-memory state of Pod checkpoint and
// restore operations and their recovery.
type podCheckpointState struct {
	checkCriuOnce sync.Once
	checkCriuErr  error

	// podCheckpointsInProgress is keyed by podCheckpointKey.
	podCheckpointsInProgress sync.Map

	recoveryMu      sync.Mutex
	recoveryRunning bool
	recoveryPending bool
}

const (
	podCheckpointManifestVersion = 2
	podCheckpointManifestFile    = "checkpoint-manifest.json"
	podCheckpointConfigFile      = "pod-config.json"
	checkpointArchiveTempPrefix  = ".checkpoint-image-"
	checkpointMetadataTempPrefix = ".checkpoint-metadata-"
)

type podCheckpointManifest struct {
	Version    int                              `json:"version"`
	SandboxID  string                           `json:"sandboxId"`
	Containers []podCheckpointManifestContainer `json:"containers"`
}

type podCheckpointManifestContainer struct {
	Name    string          `json:"name"`
	ID      string          `json:"id"`
	Archive string          `json:"archive"`
	Config  json.RawMessage `json:"config"`
	Status  json.RawMessage `json:"status"`
}

type checkpointContainer struct {
	id        string
	name      string
	config    *runtime.ContainerConfig
	manifest  podCheckpointManifestContainer
	container client.Container
	task      client.Task
}

func (c *CheckpointService) checkCriu() error {
	c.checkCriuOnce.Do(func() {
		if err := ctrdutil.CheckCriu(c.shimPath); err != nil {
			c.checkCriuErr = fmt.Errorf("%w: %w", err, errdefs.ErrFailedPrecondition)
		}
	})
	return c.checkCriuErr
}

func (c *CheckpointService) Checkpoint(ctx context.Context, sandboxID string, opts sandbox.CheckpointOptions) error {
	if sandboxID == "" {
		return fmt.Errorf("sandbox ID is required for checkpoint: %w", errdefs.ErrInvalidArgument)
	}
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	if err := validatePodCheckpointOptions(opts.Options); err != nil {
		return err
	}
	outputPath, err := resolvePodCheckpointOutput(opts.OutputPath)
	if err != nil {
		return err
	}
	key := podCheckpointKey(namespace, sandboxID)
	if _, loaded := c.podCheckpointsInProgress.LoadOrStore(key, struct{}{}); loaded {
		return fmt.Errorf("checkpoint for pod sandbox %q is already in progress: %w", sandboxID, errdefs.ErrAlreadyExists)
	}
	defer c.podCheckpointsInProgress.Delete(key)

	sandboxConfig, containers, err := c.prepareCheckpointContainers(ctx, sandboxID, opts)
	if err != nil {
		return err
	}
	// Check CRIU after the runtimes, so a runtime this backend cannot
	// checkpoint is reported as such rather than as missing CRIU.
	if err := c.checkCriu(); err != nil {
		return err
	}
	freezer, err := loadPodCgroupFreezer(sandboxConfig.GetLinux().GetCgroupParent())
	if err != nil {
		return err
	}

	completed := false
	defer func() {
		if !completed {
			if err := cleanupPartialCheckpoint(outputPath); err != nil {
				log.G(ctx).WithError(err).Errorf("failed to clean partial Pod checkpoint %q", outputPath)
			}
		}
	}()
	configData, err := json.Marshal(sandboxConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox config: %w", err)
	}
	containerIDs := make([]string, 0, len(containers))
	tasks := make([]podCheckpointRuntimeTask, 0, len(containers))
	for _, container := range containers {
		containerIDs = append(containerIDs, container.id)
		tasks = append(tasks, container.task)
	}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		Namespace:    namespace,
		SandboxID:    sandboxID,
		CgroupParent: sandboxConfig.GetLinux().GetCgroupParent(),
		ContainerIDs: containerIDs,
		OutputPath:   outputPath,
	}
	markerActive, markerErr := c.beginPodCheckpoint(ctx, freezer, marker)
	var recovery podCheckpointTaskRecovery
	tasksResumed := false
	defer func() {
		if !markerActive {
			return
		}
		cleanupCtx, cancel := podCheckpointCleanupContext(namespace)
		defer cancel()
		// A remaining marker is recovered by the next checkpoint of this
		// sandbox, by the background retry, or after a restart.
		if err := recovery.recover(cleanupCtx); err != nil {
			log.G(ctx).WithError(err).Error("failed to recover Pod after checkpoint")
			c.RecoverInBackground()
			return
		}
		if !tasksResumed {
			if err := c.markPodCheckpointResumed(marker); err != nil {
				log.G(ctx).WithError(err).Error("failed to persist resumed Pod checkpoint marker")
				c.RecoverInBackground()
				return
			}
		}
		if err := c.finishPodCheckpointCleanup(cleanupCtx, marker); err != nil {
			log.G(ctx).WithError(err).Error("failed to clean up Pod checkpoint images")
			c.RecoverInBackground()
			return
		}
		markerActive = false
	}()
	if markerErr != nil {
		return markerErr
	}
	// Record cleanup ownership before creating any checkpoint output.
	if err := writePodCheckpointMetadata(filepath.Join(outputPath, podCheckpointConfigFile), configData); err != nil {
		return fmt.Errorf("failed to write sandbox config: %w", err)
	}

	recovery, err = pausePodCheckpointTasks(ctx, freezer, tasks)
	if err != nil {
		return err
	}

	manifest := podCheckpointManifest{
		Version:    podCheckpointManifestVersion,
		SandboxID:  sandboxID,
		Containers: make([]podCheckpointManifestContainer, 0, len(containers)),
	}
	checkpointImages := make([]string, 0, len(containers))
	for _, container := range containers {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pod checkpoint aborted: %w", err)
		}
		imageName := checkpointImageName(container.id)
		checkpointImages = append(checkpointImages, imageName)
		if err := c.createContainerCheckpointImage(ctx, container, imageName); err != nil {
			return fmt.Errorf("failed to checkpoint container %q: %w", container.name, err)
		}
		manifest.Containers = append(manifest.Containers, container.manifest)
	}

	cleanupCtx, cancel := podCheckpointCleanupContext(namespace)
	if err := recovery.recover(cleanupCtx); err != nil {
		cancel()
		return fmt.Errorf("failed to resume pod after checkpoint: %w", err)
	}
	cancel()
	if err := c.markPodCheckpointResumed(marker); err != nil {
		return err
	}
	tasksResumed = true

	// Exporting the content-addressed checkpoint images does not require the
	// processes to remain paused. Keep this work outside the Pod freeze window.
	for i, container := range containers {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pod checkpoint export aborted: %w", err)
		}
		archivePath := filepath.Join(outputPath, container.manifest.Archive)
		if err := c.exportCheckpointImageAtomic(ctx, checkpointImages[i], archivePath); err != nil {
			return fmt.Errorf("failed to export checkpoint for container %q: %w", container.name, err)
		}
	}

	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint manifest: %w", err)
	}
	if err := writePodCheckpointMetadata(filepath.Join(outputPath, podCheckpointManifestFile), manifestData); err != nil {
		return fmt.Errorf("failed to write checkpoint manifest: %w", err)
	}
	completed = true
	return nil
}

// Restore is not implemented yet. The controller registers the checkpoint
// capability so that Pod checkpoints can be taken through the sandbox API.
func (c *CheckpointService) Restore(context.Context, string, sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
	return sandbox.RestoreResult{}, fmt.Errorf("pod restore is not implemented by the pause controller: %w", errdefs.ErrNotImplemented)
}

// podCheckpointCleanupContext returns a context for cleanup that must run even
// when the request context is canceled, in the namespace of the request.
func podCheckpointCleanupContext(namespace string) (context.Context, context.CancelFunc) {
	ctx, cancel := ctrdutil.DeferContext()
	return namespaces.WithNamespace(ctx, namespace), cancel
}

// writePodCheckpointMetadata persists the file contents before publishing the
// name, then persists the directory entry before reporting success.
func writePodCheckpointMetadata(path string, data []byte) error {
	return writeCheckpointFileAtomic(path, checkpointMetadataTempPrefix, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

func (c *CheckpointService) prepareCheckpointContainers(ctx context.Context, sandboxID string, opts sandbox.CheckpointOptions) (*runtime.PodSandboxConfig, []checkpointContainer, error) {
	if opts.SandboxConfig == nil {
		return nil, nil, fmt.Errorf("sandbox config is required for checkpoint: %w", errdefs.ErrInvalidArgument)
	}
	sandboxConfig := new(runtime.PodSandboxConfig)
	if err := typeurl.UnmarshalTo(opts.SandboxConfig, sandboxConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode sandbox checkpoint config: %v: %w", err, errdefs.ErrInvalidArgument)
	}
	if len(opts.Containers) == 0 {
		return nil, nil, fmt.Errorf("at least one container is required for pod checkpoint: %w", errdefs.ErrInvalidArgument)
	}
	seenIDs := make(map[string]struct{}, len(opts.Containers))
	seenNames := make(map[string]struct{}, len(opts.Containers))
	containers := make([]checkpointContainer, 0, len(opts.Containers))
	for i, input := range opts.Containers {
		if input.ID == "" {
			return nil, nil, fmt.Errorf("checkpoint container at index %d has no ID: %w", i, errdefs.ErrInvalidArgument)
		}
		if _, ok := seenIDs[input.ID]; ok {
			return nil, nil, fmt.Errorf("checkpoint container ID %q is duplicated: %w", input.ID, errdefs.ErrInvalidArgument)
		}
		seenIDs[input.ID] = struct{}{}
		if input.Name == "" {
			return nil, nil, fmt.Errorf("checkpoint container %q has no name: %w", input.ID, errdefs.ErrInvalidArgument)
		}
		if _, ok := seenNames[input.Name]; ok {
			return nil, nil, fmt.Errorf("checkpoint container name %q is duplicated: %w", input.Name, errdefs.ErrInvalidArgument)
		}
		seenNames[input.Name] = struct{}{}

		config := new(runtime.ContainerConfig)
		if input.Config == nil {
			return nil, nil, fmt.Errorf("checkpoint container %q has no config: %w", input.ID, errdefs.ErrInvalidArgument)
		}
		if err := typeurl.UnmarshalTo(input.Config, config); err != nil {
			return nil, nil, fmt.Errorf("failed to decode checkpoint container %q config: %v: %w", input.ID, err, errdefs.ErrInvalidArgument)
		}
		if config.GetMetadata().GetName() != input.Name {
			return nil, nil, fmt.Errorf("checkpoint container %q config name %q does not match %q: %w", input.ID, config.GetMetadata().GetName(), input.Name, errdefs.ErrInvalidArgument)
		}
		status := new(runtime.ContainerStatus)
		if input.Status == nil {
			return nil, nil, fmt.Errorf("checkpoint container %q has no status: %w", input.ID, errdefs.ErrInvalidArgument)
		}
		if err := typeurl.UnmarshalTo(input.Status, status); err != nil {
			return nil, nil, fmt.Errorf("failed to decode checkpoint container %q status: %v: %w", input.ID, err, errdefs.ErrInvalidArgument)
		}
		if status.GetState() != runtime.ContainerState_CONTAINER_RUNNING {
			return nil, nil, fmt.Errorf("checkpoint container %q must be running, found state %s: %w", input.ID, status.GetState(), errdefs.ErrFailedPrecondition)
		}

		container, err := c.client.LoadContainer(ctx, input.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load checkpoint container %q: %w", input.ID, err)
		}
		info, err := container.Info(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to inspect checkpoint container %q: %w", input.ID, err)
		}
		if info.SandboxID != sandboxID {
			return nil, nil, fmt.Errorf("checkpoint container %q belongs to sandbox %q, not %q: %w", input.ID, info.SandboxID, sandboxID, errdefs.ErrInvalidArgument)
		}
		if err := checkPodCheckpointRuntime(input.ID, info.Runtime.Name); err != nil {
			return nil, nil, err
		}
		if err := checkPodCgroupMember(ctx, container, sandboxConfig.GetLinux().GetCgroupParent()); err != nil {
			return nil, nil, err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load checkpoint task for container %q: %w", input.ID, err)
		}
		taskStatus, err := task.Status(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to inspect checkpoint task for container %q: %w", input.ID, err)
		}
		if taskStatus.Status != client.Running {
			return nil, nil, fmt.Errorf("checkpoint task for container %q must be running, found state %s: %w", input.ID, taskStatus.Status, errdefs.ErrFailedPrecondition)
		}
		configData, err := json.Marshal(config)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal checkpoint container %q config: %w", input.ID, err)
		}
		statusData, err := json.Marshal(status)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal checkpoint container %q status: %w", input.ID, err)
		}
		containers = append(containers, checkpointContainer{
			id:        input.ID,
			name:      input.Name,
			config:    config,
			container: container,
			task:      task,
			manifest: podCheckpointManifestContainer{
				Name:    input.Name,
				ID:      input.ID,
				Archive: checkpointArchiveName(input.ID),
				Config:  configData,
				Status:  statusData,
			},
		})
	}
	// The freezer acts on the cgroup parent from the caller's sandbox config.
	// Check that it is the cgroup the pause container was created in, so a
	// wrong config cannot freeze or thaw an unrelated cgroup subtree.
	sandboxContainer, err := c.client.LoadContainer(ctx, sandboxID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load sandbox container %q: %w", sandboxID, err)
	}
	if err := checkPodCgroupMember(ctx, sandboxContainer, sandboxConfig.GetLinux().GetCgroupParent()); err != nil {
		return nil, nil, err
	}
	return sandboxConfig, containers, nil
}

// checkPodCheckpointRuntime rejects runtimes other than the runc v2 shim before
// the Pod is frozen. Other shims need not implement task pause and checkpoint
// in the way this backend uses them: the runsc shim, for example, cannot report
// task state while the Pod cgroup is frozen and does not implement checkpoint.
func checkPodCheckpointRuntime(containerID, runtimeName string) error {
	if runtimeName != plugins.RuntimeRuncV2 {
		return fmt.Errorf("checkpoint container %q uses runtime %q, but the pause-container backend supports only %q: %w", containerID, runtimeName, plugins.RuntimeRuncV2, errdefs.ErrNotImplemented)
	}
	return nil
}

// checkPodCgroupMember checks that a container was created in the pod cgroup.
func checkPodCgroupMember(ctx context.Context, container podCgroupMember, cgroupParent string) error {
	spec, err := container.Spec(ctx)
	if err != nil {
		return fmt.Errorf("failed to read spec of container %q: %w", container.ID(), err)
	}
	var cgroupsPath string
	if spec.Linux != nil {
		cgroupsPath = spec.Linux.CgroupsPath
	}
	if want := getCgroupsPath(cgroupParent, container.ID()); cgroupsPath != want {
		return fmt.Errorf("container %q cgroup %q is not in pod cgroup %q: %w", container.ID(), cgroupsPath, cgroupParent, errdefs.ErrInvalidArgument)
	}
	return nil
}

type podCgroupMember interface {
	ID() string
	Spec(context.Context) (*oci.Spec, error)
}

func (c *CheckpointService) createContainerCheckpointImage(ctx context.Context, container checkpointContainer, imageName string) error {
	workDir, err := os.MkdirTemp(c.rootDir, "checkpoint-work-")
	if err != nil {
		return fmt.Errorf("failed to create checkpoint work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	spec, err := container.container.Spec(ctx)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint container spec: %w", err)
	}
	if spec.Linux != nil {
		// CRIU passes its log fd to the parasite in the container process.
		// Its files need the container's mount label so that process can write.
		if err := label.Relabel(workDir, spec.Linux.MountLabel, false); err != nil {
			return fmt.Errorf("failed to label checkpoint work directory: %w", err)
		}
	}

	if err := c.client.ImageService().Delete(ctx, imageName, images.SynchronousDelete()); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to replace temporary checkpoint image %q: %w", imageName, err)
	}
	_, err = container.container.Checkpoint(ctx, imageName,
		withCheckpointWorkPath(workDir),
		client.WithCheckpointRuntime,
		client.WithCheckpointTask,
		// CRIU can finish an interrupted filesystem operation while seizing
		// tasks. Capture the writable layer after that operation completes.
		client.WithCheckpointRW,
	)
	return err
}

func withCheckpointWorkPath(path string) client.CheckpointOpts {
	return func(_ context.Context, _ *client.Client, _ *containers.Container, _ *imagespec.Index, opts *runcoptions.CheckpointOptions) error {
		opts.WorkPath = path
		return nil
	}
}

func (c *CheckpointService) exportCheckpointImageAtomic(ctx context.Context, imageName, destination string) error {
	return writeCheckpointFileAtomic(destination, checkpointArchiveTempPrefix, func(w io.Writer) error {
		return c.client.Export(ctx, w,
			imagearchive.WithImage(c.client.ImageService(), imageName),
			imagearchive.WithSkipDockerManifest(),
		)
	})
}

// writeCheckpointFileAtomic uses recognizable temporary names so recovery can
// remove files left by a crash before publication.
func writeCheckpointFileAtomic(destination, prefix string, write func(io.Writer) error) (retErr error) {
	temp, err := os.CreateTemp(filepath.Dir(destination), prefix)
	if err != nil {
		return fmt.Errorf("failed to create temporary checkpoint file: %w", err)
	}
	tempPath := temp.Name()
	tempClosed := false
	defer func() {
		if !tempClosed {
			retErr = errors.Join(retErr, temp.Close())
		}
		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, err)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if err := write(temp); err != nil {
		return fmt.Errorf("failed to write checkpoint file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	tempClosed = true
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("failed to publish checkpoint file %q: %w", destination, err)
	}
	return syncDirectory(filepath.Dir(destination))
}

func validateCheckpointOutputPath(checkpointDir string) error {
	if checkpointDir == "" {
		return fmt.Errorf("checkpoint output path is required: %w", errdefs.ErrInvalidArgument)
	}
	if !filepath.IsAbs(checkpointDir) {
		return fmt.Errorf("checkpoint output path %q must be absolute: %w", checkpointDir, errdefs.ErrInvalidArgument)
	}
	info, err := os.Lstat(checkpointDir)
	if err != nil {
		return fmt.Errorf("checkpoint output path %q must be an existing directory: %v: %w", checkpointDir, err, errdefs.ErrInvalidArgument)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("checkpoint output path %q is not a real directory: %w", checkpointDir, errdefs.ErrInvalidArgument)
	}
	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint output directory %q: %w", checkpointDir, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("checkpoint output directory %q must be empty: %w", checkpointDir, errdefs.ErrFailedPrecondition)
	}
	return nil
}

// resolvePodCheckpointOutput validates the output directory and returns its
// canonical path, which the recovery marker records.
func resolvePodCheckpointOutput(checkpointDir string) (string, error) {
	if err := validateCheckpointOutputPath(checkpointDir); err != nil {
		return "", err
	}
	canonicalDir, err := filepath.EvalSymlinks(checkpointDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve checkpoint output path %q: %v: %w", checkpointDir, err, errdefs.ErrInvalidArgument)
	}
	return filepath.Clean(canonicalDir), nil
}

func cleanupPartialCheckpoint(checkpointDir string) error {
	info, err := os.Lstat(checkpointDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("checkpoint output path %q is no longer a directory", checkpointDir)
	}
	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(checkpointDir, entry.Name())); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %q: %w", entry.Name(), err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func checkpointArchiveName(containerID string) string {
	return fmt.Sprintf("container-%x.tar", sha256.Sum256([]byte(containerID)))
}

func checkpointImageName(containerID string) string {
	return fmt.Sprintf("localhost/containerd-pod-checkpoint:%x", sha256.Sum256([]byte(containerID)))
}

func validatePodCheckpointOptions(options map[string]string) error {
	return rejectUnsupportedPodOptions("pod checkpoint", options)
}

func rejectUnsupportedPodOptions(operation string, options map[string]string) error {
	if len(options) == 0 {
		return nil
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Errorf("%s options %q are not supported by the pause controller: %w", operation, keys, errdefs.ErrInvalidArgument)
}
