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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	imagearchive "github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/containerd/v2/core/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/distribution/reference"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	podCheckpointManifestVersion = 2
	podCheckpointManifestFile    = "checkpoint-manifest.json"
	podCheckpointConfigFile      = "pod-config.json"
	checkpointRuntimeNameLabel   = "io.containerd.checkpoint.runtime"
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

type restoreContainer struct {
	id       string
	name     string
	config   *runtime.ContainerConfig
	archive  string
	manifest podCheckpointManifestContainer
}

func (c *CheckpointService) Checkpoint(ctx context.Context, sandboxID string, opts sandbox.CheckpointOptions) (_ error) {
	if sandboxID == "" {
		return errors.New("sandbox ID is required for checkpoint")
	}
	if err := validatePodCheckpointOptions(opts.Options); err != nil {
		return err
	}
	releaseOutput, err := c.reservePodCheckpointOutput(opts.OutputPath)
	if err != nil {
		return err
	}
	defer releaseOutput()
	if _, loaded := c.podCheckpointsInProgress.LoadOrStore(sandboxID, struct{}{}); loaded {
		return fmt.Errorf("checkpoint for pod sandbox %q is already in progress", sandboxID)
	}
	defer c.podCheckpointsInProgress.Delete(sandboxID)

	sandboxConfig, containers, err := c.prepareCheckpointContainers(ctx, sandboxID, opts)
	if err != nil {
		return err
	}
	freezer, err := loadPodCgroupFreezer(sandboxConfig.GetLinux().GetCgroupParent())
	if err != nil {
		return err
	}

	completed := false
	defer func() {
		if !completed {
			if err := cleanupPartialCheckpoint(opts.OutputPath); err != nil {
				log.G(ctx).WithError(err).Errorf("failed to clean partial Pod checkpoint %q", opts.OutputPath)
			}
		}
	}()
	configData, err := json.Marshal(sandboxConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(opts.OutputPath, podCheckpointConfigFile), configData, 0o600); err != nil {
		return fmt.Errorf("failed to write sandbox config: %w", err)
	}

	containerIDs := make([]string, 0, len(containers))
	tasks := make([]podCheckpointRuntimeTask, 0, len(containers))
	for _, container := range containers {
		containerIDs = append(containerIDs, container.id)
		tasks = append(tasks, container.task)
	}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		SandboxID:    sandboxID,
		CgroupParent: sandboxConfig.GetLinux().GetCgroupParent(),
		ContainerIDs: containerIDs,
	}
	markerActive, markerErr := c.writePodCheckpointMarker(marker)
	defer func() {
		if !markerActive {
			return
		}
		cleanupCtx, cancel := ctrdutil.DeferContext()
		defer cancel()
		if err := recoverPodCheckpointTasks(cleanupCtx, freezer, tasks); err != nil {
			log.G(ctx).WithError(err).Error("failed to recover Pod after checkpoint")
			return
		}
		if err := c.removePodCheckpointMarker(sandboxID); err != nil {
			log.G(ctx).WithError(err).Error("failed to remove Pod checkpoint recovery marker")
			return
		}
		markerActive = false
	}()
	if markerErr != nil {
		return markerErr
	}
	if err := pausePodCheckpointTasks(ctx, freezer, tasks); err != nil {
		return err
	}

	manifest := podCheckpointManifest{
		Version:    podCheckpointManifestVersion,
		SandboxID:  sandboxID,
		Containers: make([]podCheckpointManifestContainer, 0, len(containers)),
	}
	for _, container := range containers {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pod checkpoint aborted: %w", err)
		}
		archivePath := filepath.Join(opts.OutputPath, container.manifest.Archive)
		if err := c.exportContainerCheckpoint(ctx, container, archivePath); err != nil {
			return fmt.Errorf("failed to checkpoint container %q: %w", container.name, err)
		}
		manifest.Containers = append(manifest.Containers, container.manifest)
	}

	cleanupCtx, cancel := ctrdutil.DeferContext()
	if err := recoverPodCheckpointTasks(cleanupCtx, freezer, tasks); err != nil {
		cancel()
		return fmt.Errorf("failed to resume pod after checkpoint: %w", err)
	}
	cancel()
	if err := c.removePodCheckpointMarker(sandboxID); err != nil {
		return err
	}
	markerActive = false

	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(opts.OutputPath, podCheckpointManifestFile), manifestData, 0o600); err != nil {
		return fmt.Errorf("failed to write checkpoint manifest: %w", err)
	}
	if err := syncDirectory(opts.OutputPath); err != nil {
		return fmt.Errorf("failed to persist checkpoint output: %w", err)
	}
	completed = true
	return nil
}

func (c *CheckpointService) Restore(ctx context.Context, sandboxID string, opts sandbox.RestoreOptions) (_ sandbox.RestoreResult, retErr error) {
	if sandboxID == "" {
		return sandbox.RestoreResult{}, errors.New("sandbox ID is required for restore")
	}
	if err := validatePodRestoreOptions(opts.Options); err != nil {
		return sandbox.RestoreResult{}, err
	}
	containers, err := prepareRestoreContainers(opts)
	if err != nil {
		return sandbox.RestoreResult{}, err
	}

	result := sandbox.RestoreResult{
		RestoredContainers: make([]sandbox.RestoredContainer, 0, len(containers)),
	}
	var importedImages []string
	defer func() {
		if retErr == nil {
			return
		}
		cleanupCtx, cancel := ctrdutil.DeferContext()
		defer cancel()
		for _, image := range importedImages {
			if err := c.client.ImageService().Delete(cleanupCtx, image); err != nil && !errdefs.IsNotFound(err) {
				retErr = errors.Join(retErr, fmt.Errorf("failed to remove checkpoint image %q: %w", image, err))
			}
		}
	}()
	for _, container := range containers {
		if err := ctx.Err(); err != nil {
			return sandbox.RestoreResult{}, fmt.Errorf("pod restore aborted before preparing container %q: %w", container.name, err)
		}
		checkpointImage := restoreCheckpointImageName(sandboxID, container.id)
		if err := c.importContainerCheckpoint(ctx, container, checkpointImage); err != nil {
			return sandbox.RestoreResult{}, fmt.Errorf("failed to prepare restored container %q: %w", container.name, err)
		}
		importedImages = append(importedImages, checkpointImage)
		result.RestoredContainers = append(result.RestoredContainers, sandbox.RestoredContainer{
			Name:                container.name,
			TaskCheckpointImage: checkpointImage,
		})
	}
	return result, nil
}

func (c *CheckpointService) prepareCheckpointContainers(ctx context.Context, sandboxID string, opts sandbox.CheckpointOptions) (*runtime.PodSandboxConfig, []checkpointContainer, error) {
	if opts.SandboxConfig == nil {
		return nil, nil, errors.New("sandbox config is required for checkpoint")
	}
	sandboxConfig := new(runtime.PodSandboxConfig)
	if err := typeurl.UnmarshalTo(opts.SandboxConfig, sandboxConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode sandbox checkpoint config: %w", err)
	}
	if len(opts.Containers) == 0 {
		return nil, nil, errors.New("at least one container is required for pod checkpoint")
	}
	seenIDs := make(map[string]struct{}, len(opts.Containers))
	seenNames := make(map[string]struct{}, len(opts.Containers))
	containers := make([]checkpointContainer, 0, len(opts.Containers))
	for i, input := range opts.Containers {
		if input.ID == "" {
			return nil, nil, fmt.Errorf("checkpoint container at index %d has no ID", i)
		}
		if _, ok := seenIDs[input.ID]; ok {
			return nil, nil, fmt.Errorf("checkpoint container ID %q is duplicated", input.ID)
		}
		seenIDs[input.ID] = struct{}{}
		if input.Name == "" {
			return nil, nil, fmt.Errorf("checkpoint container %q has no name", input.ID)
		}
		if _, ok := seenNames[input.Name]; ok {
			return nil, nil, fmt.Errorf("checkpoint container name %q is duplicated", input.Name)
		}
		seenNames[input.Name] = struct{}{}

		config := new(runtime.ContainerConfig)
		if input.Config == nil {
			return nil, nil, fmt.Errorf("checkpoint container %q has no config", input.ID)
		}
		if err := typeurl.UnmarshalTo(input.Config, config); err != nil {
			return nil, nil, fmt.Errorf("failed to decode checkpoint container %q config: %w", input.ID, err)
		}
		if config.GetMetadata().GetName() != input.Name {
			return nil, nil, fmt.Errorf("checkpoint container %q config name %q does not match %q", input.ID, config.GetMetadata().GetName(), input.Name)
		}
		status := new(runtime.ContainerStatus)
		if input.Status == nil {
			return nil, nil, fmt.Errorf("checkpoint container %q has no status", input.ID)
		}
		if err := typeurl.UnmarshalTo(input.Status, status); err != nil {
			return nil, nil, fmt.Errorf("failed to decode checkpoint container %q status: %w", input.ID, err)
		}
		if status.GetState() != runtime.ContainerState_CONTAINER_RUNNING {
			return nil, nil, fmt.Errorf("checkpoint container %q must be running, found state %s", input.ID, status.GetState())
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
			return nil, nil, fmt.Errorf("checkpoint container %q belongs to sandbox %q, not %q", input.ID, info.SandboxID, sandboxID)
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
			return nil, nil, fmt.Errorf("checkpoint task for container %q must be running, found state %s", input.ID, taskStatus.Status)
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
	return sandboxConfig, containers, nil
}

func (c *CheckpointService) exportContainerCheckpoint(ctx context.Context, container checkpointContainer, destination string) error {
	workDir, err := os.MkdirTemp(c.rootDir, "checkpoint-work-")
	if err != nil {
		return fmt.Errorf("failed to create checkpoint work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	imageName := checkpointImageName(container.id)
	if err := c.client.ImageService().Delete(ctx, imageName); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to replace temporary checkpoint image %q: %w", imageName, err)
	}
	defer func() {
		if err := c.client.ImageService().Delete(ctx, imageName); err != nil && !errdefs.IsNotFound(err) {
			log.G(ctx).WithError(err).Warnf("failed to remove temporary checkpoint image %q", imageName)
		}
	}()
	checkpoint, err := container.container.Checkpoint(ctx, imageName,
		withCheckpointWorkPath(workDir),
		client.WithCheckpointRuntime,
		client.WithCheckpointRW,
		client.WithCheckpointTask,
	)
	if err != nil {
		return err
	}
	return c.exportCheckpointImageAtomic(ctx, checkpoint.Name(), destination)
}

func withCheckpointWorkPath(path string) client.CheckpointOpts {
	return func(_ context.Context, _ *client.Client, _ *containers.Container, _ *imagespec.Index, opts *runcoptions.CheckpointOptions) error {
		opts.WorkPath = path
		return nil
	}
}

func (c *CheckpointService) exportCheckpointImageAtomic(ctx context.Context, imageName, destination string) (retErr error) {
	temp, err := os.CreateTemp(filepath.Dir(destination), ".checkpoint-image-")
	if err != nil {
		return fmt.Errorf("failed to create temporary checkpoint archive: %w", err)
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
	if err := c.client.Export(ctx, temp,
		imagearchive.WithImage(c.client.ImageService(), imageName),
		imagearchive.WithSkipDockerManifest(),
	); err != nil {
		return fmt.Errorf("failed to export checkpoint image: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	tempClosed = true
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("failed to publish checkpoint archive %q: %w", destination, err)
	}
	return syncDirectory(filepath.Dir(destination))
}

func prepareRestoreContainers(opts sandbox.RestoreOptions) ([]restoreContainer, error) {
	if opts.CheckpointPath == "" {
		return nil, errors.New("checkpoint path is required")
	}
	if !filepath.IsAbs(opts.CheckpointPath) {
		return nil, fmt.Errorf("checkpoint path %q must be absolute", opts.CheckpointPath)
	}
	info, err := os.Lstat(opts.CheckpointPath)
	if err != nil {
		return nil, fmt.Errorf("checkpoint path %q must be an existing directory: %w", opts.CheckpointPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("checkpoint path %q is not a real directory", opts.CheckpointPath)
	}
	if opts.SandboxConfig == nil {
		return nil, errors.New("sandbox config is required for restore")
	}
	restoreSandboxConfig := new(runtime.PodSandboxConfig)
	if err := typeurl.UnmarshalTo(opts.SandboxConfig, restoreSandboxConfig); err != nil {
		return nil, fmt.Errorf("failed to decode sandbox restore config: %w", err)
	}
	checkpointSandboxConfig, err := readPodCheckpointConfig(opts.CheckpointPath)
	if err != nil {
		return nil, err
	}
	if err := validateRestoreSandboxConfig(checkpointSandboxConfig, restoreSandboxConfig); err != nil {
		return nil, err
	}
	manifest, err := readPodCheckpointManifest(opts.CheckpointPath)
	if err != nil {
		return nil, err
	}
	if len(opts.Containers) == 0 {
		return nil, errors.New("at least one container is required for pod restore")
	}
	manifestByName := make(map[string]podCheckpointManifestContainer, len(manifest.Containers))
	seenManifestIDs := make(map[string]struct{}, len(manifest.Containers))
	for i, container := range manifest.Containers {
		if container.Name == "" || container.ID == "" {
			return nil, fmt.Errorf("checkpoint manifest container at index %d has incomplete identity", i)
		}
		if _, ok := manifestByName[container.Name]; ok {
			return nil, fmt.Errorf("checkpoint manifest contains duplicate container name %q", container.Name)
		}
		if _, ok := seenManifestIDs[container.ID]; ok {
			return nil, fmt.Errorf("checkpoint manifest contains duplicate container ID %q", container.ID)
		}
		seenManifestIDs[container.ID] = struct{}{}
		expectedArchive := checkpointArchiveName(container.ID)
		if container.Archive != expectedArchive {
			return nil, fmt.Errorf("checkpoint manifest container %q has invalid archive name %q; expected %q", container.Name, container.Archive, expectedArchive)
		}
		manifestByName[container.Name] = container
	}

	seenIDs := make(map[string]struct{}, len(opts.Containers))
	seenNames := make(map[string]struct{}, len(opts.Containers))
	containers := make([]restoreContainer, 0, len(opts.Containers))
	for i, input := range opts.Containers {
		if input.ID == "" {
			return nil, fmt.Errorf("restore container at index %d has no ID", i)
		}
		if _, ok := seenIDs[input.ID]; ok {
			return nil, fmt.Errorf("restore container ID %q is duplicated", input.ID)
		}
		seenIDs[input.ID] = struct{}{}
		if input.Name == "" {
			return nil, fmt.Errorf("restore container %q has no name", input.ID)
		}
		if _, ok := seenNames[input.Name]; ok {
			return nil, fmt.Errorf("restore container name %q is duplicated", input.Name)
		}
		seenNames[input.Name] = struct{}{}
		manifestContainer, ok := manifestByName[input.Name]
		if !ok {
			return nil, fmt.Errorf("restore container %q has no matching checkpoint entry", input.Name)
		}
		config := new(runtime.ContainerConfig)
		if input.Config == nil {
			return nil, fmt.Errorf("restore container %q has no config", input.Name)
		}
		if err := typeurl.UnmarshalTo(input.Config, config); err != nil {
			return nil, fmt.Errorf("failed to decode restore container %q config: %w", input.Name, err)
		}
		checkpointConfig := new(runtime.ContainerConfig)
		if err := json.Unmarshal(manifestContainer.Config, checkpointConfig); err != nil {
			return nil, fmt.Errorf("failed to decode checkpoint container %q config: %w", input.Name, err)
		}
		if checkpointConfig.GetMetadata().GetName() != manifestContainer.Name {
			return nil, fmt.Errorf("checkpoint container config name %q does not match manifest name %q", checkpointConfig.GetMetadata().GetName(), manifestContainer.Name)
		}
		if config.GetMetadata().GetName() != input.Name {
			return nil, fmt.Errorf("restore container config name %q does not match option name %q", config.GetMetadata().GetName(), input.Name)
		}
		if err := validateRestoreContainerConfig(checkpointConfig, config); err != nil {
			return nil, fmt.Errorf("container %q is incompatible with its checkpoint: %w", input.Name, err)
		}
		archivePath := filepath.Join(opts.CheckpointPath, manifestContainer.Archive)
		archiveInfo, err := os.Lstat(archivePath)
		if err != nil {
			return nil, fmt.Errorf("checkpoint archive for container %q is not accessible: %w", input.Name, err)
		}
		if !archiveInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("checkpoint archive for container %q is not a regular file", input.Name)
		}
		containers = append(containers, restoreContainer{
			id:       input.ID,
			name:     input.Name,
			config:   config,
			archive:  archivePath,
			manifest: manifestContainer,
		})
	}
	if len(containers) != len(manifest.Containers) {
		return nil, fmt.Errorf("restore has %d containers, but checkpoint manifest has %d", len(containers), len(manifest.Containers))
	}
	return containers, nil
}

func (c *CheckpointService) importContainerCheckpoint(ctx context.Context, container restoreContainer, imageName string) (retErr error) {
	archive, err := os.Open(container.archive)
	if err != nil {
		return err
	}
	defer archive.Close()
	if err := c.client.ImageService().Delete(ctx, imageName); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to replace checkpoint image %q: %w", imageName, err)
	}
	if _, err := c.client.Import(ctx, archive, client.WithImageRefTranslator(func(string) string {
		return imageName
	})); err != nil {
		return fmt.Errorf("failed to import checkpoint archive: %w", err)
	}
	defer func() {
		if retErr != nil {
			if err := c.client.ImageService().Delete(ctx, imageName); err != nil && !errdefs.IsNotFound(err) {
				retErr = errors.Join(retErr, err)
			}
		}
	}()
	checkpoint, err := c.client.GetImage(ctx, imageName)
	if err != nil {
		return err
	}
	indexData, err := content.ReadBlob(ctx, c.client.ContentStore(), checkpoint.Target())
	if err != nil {
		return fmt.Errorf("failed to read checkpoint image index: %w", err)
	}
	var index imagespec.Index
	if err := json.Unmarshal(indexData, &index); err != nil {
		return fmt.Errorf("failed to decode checkpoint image index: %w", err)
	}
	createdContainer, err := c.client.LoadContainer(ctx, container.id)
	if err != nil {
		return fmt.Errorf("failed to load restored container %q: %w", container.id, err)
	}
	info, err := createdContainer.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to inspect restored container %q: %w", container.id, err)
	}
	if checkpointRuntime := index.Annotations[checkpointRuntimeNameLabel]; checkpointRuntime != "" && checkpointRuntime != info.Runtime.Name {
		return fmt.Errorf("checkpoint runtime %q does not match restore runtime %q: %w", checkpointRuntime, info.Runtime.Name, errdefs.ErrFailedPrecondition)
	}
	rw, err := client.GetIndexByMediaType(&index, imagespec.MediaTypeImageLayerGzip)
	if err != nil {
		return fmt.Errorf("failed to find checkpoint writable layer: %w", err)
	}
	mounts, err := c.client.SnapshotService(info.Snapshotter).Mounts(ctx, info.SnapshotKey)
	if err != nil {
		return fmt.Errorf("failed to mount restored container snapshot: %w", err)
	}
	if _, err := c.client.DiffService().Apply(ctx, *rw, mounts); err != nil {
		return fmt.Errorf("failed to apply checkpoint writable layer: %w", err)
	}
	return nil
}

func validateCheckpointOutputPath(checkpointDir string) error {
	if checkpointDir == "" {
		return errors.New("checkpoint output path is required")
	}
	if !filepath.IsAbs(checkpointDir) {
		return fmt.Errorf("checkpoint output path %q must be absolute", checkpointDir)
	}
	info, err := os.Lstat(checkpointDir)
	if err != nil {
		return fmt.Errorf("checkpoint output path %q must be an existing directory: %w", checkpointDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("checkpoint output path %q is not a real directory", checkpointDir)
	}
	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint output directory %q: %w", checkpointDir, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("checkpoint output directory %q must be empty", checkpointDir)
	}
	return nil
}

func (c *CheckpointService) reservePodCheckpointOutput(checkpointDir string) (func(), error) {
	if err := validateCheckpointOutputPath(checkpointDir); err != nil {
		return nil, err
	}
	canonicalDir, err := filepath.EvalSymlinks(checkpointDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve checkpoint output path %q: %w", checkpointDir, err)
	}
	canonicalDir = filepath.Clean(canonicalDir)
	if _, loaded := c.podCheckpointOutputsInProgress.LoadOrStore(canonicalDir, struct{}{}); loaded {
		return nil, fmt.Errorf("checkpoint output directory %q is already in use by another checkpoint", checkpointDir)
	}
	return func() {
		c.podCheckpointOutputsInProgress.Delete(canonicalDir)
	}, nil
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

func restoreCheckpointImageName(sandboxID, containerID string) string {
	return fmt.Sprintf("localhost/containerd-pod-restore:%x", sha256.Sum256([]byte(sandboxID+"\x00"+containerID)))
}

func readPodCheckpointManifest(checkpointDir string) (podCheckpointManifest, error) {
	path := filepath.Join(checkpointDir, podCheckpointManifestFile)
	info, err := os.Lstat(path)
	if err != nil {
		return podCheckpointManifest{}, fmt.Errorf("failed to inspect checkpoint manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return podCheckpointManifest{}, errors.New("checkpoint manifest is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return podCheckpointManifest{}, fmt.Errorf("failed to read checkpoint manifest: %w", err)
	}
	manifest, err := decodePodCheckpointManifest(data)
	if err != nil {
		return podCheckpointManifest{}, fmt.Errorf("failed to decode checkpoint manifest: %w", err)
	}
	if manifest.Version != podCheckpointManifestVersion {
		return podCheckpointManifest{}, fmt.Errorf("checkpoint manifest version %d is unsupported; expected %d", manifest.Version, podCheckpointManifestVersion)
	}
	if manifest.SandboxID == "" {
		return podCheckpointManifest{}, errors.New("checkpoint manifest has no sandbox ID")
	}
	if len(manifest.Containers) == 0 {
		return podCheckpointManifest{}, errors.New("checkpoint manifest contains no containers")
	}
	return manifest, nil
}

func decodePodCheckpointManifest(data []byte) (podCheckpointManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest podCheckpointManifest
	if err := decoder.Decode(&manifest); err != nil {
		return podCheckpointManifest{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return manifest, nil
	} else if err != nil {
		return podCheckpointManifest{}, err
	}
	return podCheckpointManifest{}, errors.New("checkpoint manifest contains multiple JSON values")
}

func readPodCheckpointConfig(checkpointDir string) (*runtime.PodSandboxConfig, error) {
	path := filepath.Join(checkpointDir, podCheckpointConfigFile)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect checkpoint sandbox config: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("checkpoint sandbox config is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint sandbox config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	config := new(runtime.PodSandboxConfig)
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("failed to decode checkpoint sandbox config: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return config, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to decode checkpoint sandbox config: %w", err)
	}
	return nil, errors.New("checkpoint sandbox config contains multiple JSON values")
}

func validateRestoreSandboxConfig(checkpoint, restore *runtime.PodSandboxConfig) error {
	if checkpoint.GetHostname() != restore.GetHostname() {
		return fmt.Errorf("restore sandbox hostname %q does not match checkpoint hostname %q: %w", restore.GetHostname(), checkpoint.GetHostname(), errdefs.ErrFailedPrecondition)
	}
	checkpointSecurity := checkpoint.GetLinux().GetSecurityContext()
	if checkpointSecurity == nil {
		checkpointSecurity = new(runtime.LinuxSandboxSecurityContext)
	}
	restoreSecurity := restore.GetLinux().GetSecurityContext()
	if restoreSecurity == nil {
		restoreSecurity = new(runtime.LinuxSandboxSecurityContext)
	}
	if !proto.Equal(checkpointSecurity, restoreSecurity) {
		return fmt.Errorf("restore sandbox security context does not match checkpoint sandbox security context: %w", errdefs.ErrFailedPrecondition)
	}
	if !maps.Equal(checkpoint.GetLinux().GetSysctls(), restore.GetLinux().GetSysctls()) {
		return fmt.Errorf("restore sandbox sysctls do not match checkpoint sandbox sysctls: %w", errdefs.ErrFailedPrecondition)
	}
	checkpointPorts := checkpoint.GetPortMappings()
	restorePorts := restore.GetPortMappings()
	if len(checkpointPorts) != len(restorePorts) {
		return fmt.Errorf("restore sandbox port mappings do not match checkpoint sandbox port mappings: %w", errdefs.ErrFailedPrecondition)
	}
	for i := range checkpointPorts {
		if !proto.Equal(checkpointPorts[i], restorePorts[i]) {
			return fmt.Errorf("restore sandbox port mappings do not match checkpoint sandbox port mappings: %w", errdefs.ErrFailedPrecondition)
		}
	}
	return nil
}

func validateRestoreContainerConfig(checkpoint, restore *runtime.ContainerConfig) error {
	if err := validateCheckpointImage(containerImageName(restore), containerImageName(checkpoint)); err != nil {
		return err
	}
	if !slices.Equal(checkpoint.GetCommand(), restore.GetCommand()) {
		return fmt.Errorf("restore command %q does not match checkpoint command %q: %w", restore.GetCommand(), checkpoint.GetCommand(), errdefs.ErrFailedPrecondition)
	}
	if !slices.Equal(checkpoint.GetArgs(), restore.GetArgs()) {
		return fmt.Errorf("restore arguments %q do not match checkpoint arguments %q: %w", restore.GetArgs(), checkpoint.GetArgs(), errdefs.ErrFailedPrecondition)
	}
	if checkpoint.GetWorkingDir() != restore.GetWorkingDir() {
		return fmt.Errorf("restore working directory %q does not match checkpoint working directory %q: %w", restore.GetWorkingDir(), checkpoint.GetWorkingDir(), errdefs.ErrFailedPrecondition)
	}
	if !maps.Equal(containerEnvironment(checkpoint), containerEnvironment(restore)) {
		return fmt.Errorf("restore environment does not match the checkpointed container environment: %w", errdefs.ErrFailedPrecondition)
	}
	if checkpoint.GetTty() != restore.GetTty() {
		return fmt.Errorf("restore TTY setting does not match the checkpointed container: %w", errdefs.ErrFailedPrecondition)
	}
	checkpointSecurity := checkpoint.GetLinux().GetSecurityContext()
	if checkpointSecurity == nil {
		checkpointSecurity = new(runtime.LinuxContainerSecurityContext)
	}
	restoreSecurity := restore.GetLinux().GetSecurityContext()
	if restoreSecurity == nil {
		restoreSecurity = new(runtime.LinuxContainerSecurityContext)
	}
	if !proto.Equal(checkpointSecurity, restoreSecurity) {
		return fmt.Errorf("restore process security context does not match the checkpointed container: %w", errdefs.ErrFailedPrecondition)
	}
	return nil
}

func containerImageName(config *runtime.ContainerConfig) string {
	if image := config.GetImage().GetUserSpecifiedImage(); image != "" {
		return image
	}
	return config.GetImage().GetImage()
}

func validateCheckpointImage(restoreImage, checkpointImage string) error {
	if restoreImage == "" || checkpointImage == "" {
		return nil
	}
	if normalizeCheckpointImage(restoreImage) == normalizeCheckpointImage(checkpointImage) {
		return nil
	}
	return fmt.Errorf("restore image %q does not match checkpoint image %q: %w", restoreImage, checkpointImage, errdefs.ErrFailedPrecondition)
}

func normalizeCheckpointImage(image string) string {
	ref, err := reference.ParseAnyReference(image)
	if err != nil {
		return image
	}
	if named, ok := ref.(reference.Named); ok {
		return reference.TagNameOnly(named).String()
	}
	return ref.String()
}

func containerEnvironment(config *runtime.ContainerConfig) map[string]string {
	env := make(map[string]string, len(config.GetEnvs()))
	for _, entry := range config.GetEnvs() {
		env[entry.GetKey()] = string(entry.GetValue())
	}
	return env
}

func validatePodCheckpointOptions(options map[string]string) error {
	return rejectUnsupportedPodOptions("pod checkpoint", options)
}

func validatePodRestoreOptions(options map[string]string) error {
	return rejectUnsupportedPodOptions("pod restore", options)
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
