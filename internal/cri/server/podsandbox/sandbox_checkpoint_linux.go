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
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	imagearchive "github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/containerd/v2/core/sandbox"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	podCheckpointManifestVersion = 2
	podCheckpointManifestFile    = "checkpoint-manifest.json"
	podCheckpointConfigFile      = "pod-config.json"
	checkpointRuntimeNameLabel   = "io.containerd.checkpoint.runtime"
	checkpointSnapshotterLabel   = "io.containerd.checkpoint.snapshotter"

	maxPodCheckpointManifestSize = 16 << 20
	maxPodCheckpointConfigSize   = 4 << 20
	maxCheckpointIndexSize       = 4 << 20
	maxCheckpointArchiveSize     = 1 << 40
	maxCheckpointArchiveEntries  = 100_000
	maxPodCheckpointContainers   = 1024
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
	archive  *os.File
	imageRef digest.Digest
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
	checkpointImages := make([]string, 0, len(containers))
	defer func() {
		cleanupCtx, cancel := ctrdutil.DeferContext()
		defer cancel()
		for _, imageName := range checkpointImages {
			if err := c.client.ImageService().Delete(cleanupCtx, imageName); err != nil && !errdefs.IsNotFound(err) {
				log.G(ctx).WithError(err).Warnf("failed to remove temporary checkpoint image %q", imageName)
			}
		}
	}()
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

	// Exporting the content-addressed checkpoint images does not require the
	// processes to remain paused. Keep this work outside the Pod freeze window.
	for i, container := range containers {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pod checkpoint export aborted: %w", err)
		}
		archivePath := filepath.Join(opts.OutputPath, container.manifest.Archive)
		if err := c.exportCheckpointImageAtomic(ctx, checkpointImages[i], archivePath); err != nil {
			return fmt.Errorf("failed to export checkpoint for container %q: %w", container.name, err)
		}
	}

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
	defer closeRestoreContainers(containers)

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

func (c *CheckpointService) createContainerCheckpointImage(ctx context.Context, container checkpointContainer, imageName string) error {
	workDir, err := os.MkdirTemp(c.rootDir, "checkpoint-work-")
	if err != nil {
		return fmt.Errorf("failed to create checkpoint work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	if err := c.client.ImageService().Delete(ctx, imageName); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to replace temporary checkpoint image %q: %w", imageName, err)
	}
	_, err = container.container.Checkpoint(ctx, imageName,
		withCheckpointWorkPath(workDir),
		client.WithCheckpointRuntime,
		client.WithCheckpointRW,
		client.WithCheckpointTask,
	)
	if err != nil {
		return err
	}
	return nil
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

func prepareRestoreContainers(opts sandbox.RestoreOptions) (_ []restoreContainer, retErr error) {
	if opts.CheckpointPath == "" {
		return nil, errors.New("checkpoint path is required")
	}
	if !filepath.IsAbs(opts.CheckpointPath) {
		return nil, fmt.Errorf("checkpoint path %q must be absolute", opts.CheckpointPath)
	}
	checkpointDir, err := openCheckpointDirectory(opts.CheckpointPath)
	if err != nil {
		return nil, err
	}
	defer checkpointDir.Close()
	if opts.SandboxConfig == nil {
		return nil, errors.New("sandbox config is required for restore")
	}
	restoreSandboxConfig := new(runtime.PodSandboxConfig)
	if err := typeurl.UnmarshalTo(opts.SandboxConfig, restoreSandboxConfig); err != nil {
		return nil, fmt.Errorf("failed to decode sandbox restore config: %w", err)
	}
	checkpointSandboxConfig, err := readPodCheckpointConfigAt(checkpointDir)
	if err != nil {
		return nil, err
	}
	if err := validateRestoreSandboxConfig(checkpointSandboxConfig, restoreSandboxConfig); err != nil {
		return nil, err
	}
	manifest, err := readPodCheckpointManifestAt(checkpointDir)
	if err != nil {
		return nil, err
	}
	if len(opts.Containers) == 0 {
		return nil, errors.New("at least one container is required for pod restore")
	}
	if len(opts.Containers) > maxPodCheckpointContainers {
		return nil, fmt.Errorf("pod restore contains %d containers; maximum is %d", len(opts.Containers), maxPodCheckpointContainers)
	}
	if len(manifest.Containers) > maxPodCheckpointContainers {
		return nil, fmt.Errorf("checkpoint manifest contains %d containers; maximum is %d", len(manifest.Containers), maxPodCheckpointContainers)
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
	defer func() {
		if retErr != nil {
			_ = closeRestoreContainers(containers)
		}
	}()
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
		if err := decodeStrictJSON(manifestContainer.Config, checkpointConfig); err != nil {
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
		checkpointStatus := new(runtime.ContainerStatus)
		if err := decodeStrictJSON(manifestContainer.Status, checkpointStatus); err != nil {
			return nil, fmt.Errorf("failed to decode checkpoint container %q status: %w", input.Name, err)
		}
		if checkpointStatus.GetId() != manifestContainer.ID {
			return nil, fmt.Errorf("checkpoint container status ID %q does not match manifest ID %q", checkpointStatus.GetId(), manifestContainer.ID)
		}
		imageRef := digest.Digest(checkpointStatus.GetImageRef())
		if err := imageRef.Validate(); err != nil {
			return nil, fmt.Errorf("checkpoint container %q has invalid image config digest %q: %w", input.Name, imageRef, err)
		}
		archive, err := openRegularFileAt(checkpointDir, manifestContainer.Archive, maxCheckpointArchiveSize)
		if err != nil {
			return nil, fmt.Errorf("checkpoint archive for container %q is not accessible: %w", input.Name, err)
		}
		containers = append(containers, restoreContainer{
			id:       input.ID,
			name:     input.Name,
			config:   config,
			archive:  archive,
			imageRef: imageRef,
			manifest: manifestContainer,
		})
	}
	if len(containers) != len(manifest.Containers) {
		return nil, fmt.Errorf("restore has %d containers, but checkpoint manifest has %d", len(containers), len(manifest.Containers))
	}
	return containers, nil
}

func (c *CheckpointService) importContainerCheckpoint(ctx context.Context, container restoreContainer, imageName string) (retErr error) {
	createdContainer, err := c.client.LoadContainer(ctx, container.id)
	if err != nil {
		return fmt.Errorf("failed to load restored container %q: %w", container.id, err)
	}
	info, err := createdContainer.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to inspect restored container %q: %w", container.id, err)
	}
	restoreImageRef, err := restoreContainerImageConfigDigest(info, container.id)
	if err != nil {
		return err
	}
	if err := validateRestoreImageConfig(container.imageRef, restoreImageRef); err != nil {
		return err
	}

	archiveInfo, err := container.archive.Stat()
	if err != nil {
		return fmt.Errorf("failed to inspect checkpoint archive: %w", err)
	}
	if err := validateCheckpointTar(io.NewSectionReader(container.archive, 0, archiveInfo.Size()), archiveInfo.Size()); err != nil {
		return fmt.Errorf("invalid checkpoint OCI archive: %w", err)
	}
	if _, err := container.archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind checkpoint archive: %w", err)
	}
	if err := c.client.ImageService().Delete(ctx, imageName); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to replace checkpoint image %q: %w", imageName, err)
	}
	defer func() {
		if retErr != nil {
			if err := c.client.ImageService().Delete(ctx, imageName); err != nil && !errdefs.IsNotFound(err) {
				retErr = errors.Join(retErr, err)
			}
		}
	}()
	leaseCtx, done, err := c.client.WithLease(ctx)
	if err != nil {
		return fmt.Errorf("failed to create checkpoint import lease: %w", err)
	}
	defer done(leaseCtx)
	outerTarget, err := imagearchive.ImportIndex(leaseCtx, c.client.ContentStore(), container.archive)
	if err != nil {
		return fmt.Errorf("failed to import checkpoint archive content: %w", err)
	}
	checkpointTarget, err := checkpointTargetFromArchiveIndex(leaseCtx, c.client.ContentStore(), outerTarget)
	if err != nil {
		return err
	}
	if _, err := c.client.ImageService().Create(leaseCtx, images.Image{
		Name:   imageName,
		Target: checkpointTarget,
	}); err != nil {
		return fmt.Errorf("failed to create deterministic checkpoint image %q: %w", imageName, err)
	}
	checkpoint, err := c.client.GetImage(ctx, imageName)
	if err != nil {
		return err
	}
	if checkpoint.Target().MediaType != imagespec.MediaTypeImageIndex {
		return fmt.Errorf("checkpoint image target has unsupported media type %q", checkpoint.Target().MediaType)
	}
	if checkpoint.Target().Size <= 0 || checkpoint.Target().Size > maxCheckpointIndexSize {
		return fmt.Errorf("checkpoint image index size %d is outside the allowed range", checkpoint.Target().Size)
	}
	indexData, err := content.ReadBlob(ctx, c.client.ContentStore(), checkpoint.Target())
	if err != nil {
		return fmt.Errorf("failed to read checkpoint image index: %w", err)
	}
	var index imagespec.Index
	if err := decodeStrictJSON(indexData, &index); err != nil {
		return fmt.Errorf("failed to decode checkpoint image index: %w", err)
	}
	taskCheckpoint, rw, err := validateCheckpointIndex(&index)
	if err != nil {
		return err
	}
	for _, desc := range index.Manifests {
		if err := validateCheckpointDescriptorContent(ctx, c.client.ContentStore(), desc); err != nil {
			return err
		}
	}
	if err := validateCheckpointTaskContent(ctx, c.client.ContentStore(), *taskCheckpoint); err != nil {
		return err
	}
	if checkpointRuntime := index.Annotations[checkpointRuntimeNameLabel]; checkpointRuntime != "" && checkpointRuntime != info.Runtime.Name {
		return fmt.Errorf("checkpoint runtime %q does not match restore runtime %q: %w", checkpointRuntime, info.Runtime.Name, errdefs.ErrFailedPrecondition)
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

func checkpointTargetFromArchiveIndex(ctx context.Context, provider content.Provider, outerTarget imagespec.Descriptor) (imagespec.Descriptor, error) {
	if outerTarget.MediaType != imagespec.MediaTypeImageIndex {
		return imagespec.Descriptor{}, fmt.Errorf("checkpoint archive target has unsupported media type %q", outerTarget.MediaType)
	}
	if outerTarget.Size <= 0 || outerTarget.Size > maxCheckpointIndexSize {
		return imagespec.Descriptor{}, fmt.Errorf("checkpoint archive index size %d is outside the allowed range", outerTarget.Size)
	}
	indexData, err := content.ReadBlob(ctx, provider, outerTarget)
	if err != nil {
		return imagespec.Descriptor{}, fmt.Errorf("failed to read checkpoint archive index: %w", err)
	}
	var index imagespec.Index
	if err := decodeStrictJSON(indexData, &index); err != nil {
		return imagespec.Descriptor{}, fmt.Errorf("failed to decode checkpoint archive index: %w", err)
	}
	return checkpointTargetFromIndex(&index)
}

func checkpointTargetFromIndex(index *imagespec.Index) (imagespec.Descriptor, error) {
	if index.SchemaVersion != 2 {
		return imagespec.Descriptor{}, fmt.Errorf("checkpoint archive index schema version %d is unsupported", index.SchemaVersion)
	}
	if len(index.Annotations) != 0 {
		return imagespec.Descriptor{}, errors.New("checkpoint archive index contains unsupported annotations")
	}
	if len(index.Manifests) != 1 {
		return imagespec.Descriptor{}, fmt.Errorf("checkpoint archive index contains %d images instead of exactly one", len(index.Manifests))
	}
	target := index.Manifests[0]
	if target.MediaType != imagespec.MediaTypeImageIndex {
		return imagespec.Descriptor{}, fmt.Errorf("checkpoint archive image has unsupported media type %q", target.MediaType)
	}
	if target.Size <= 0 || target.Size > maxCheckpointIndexSize {
		return imagespec.Descriptor{}, fmt.Errorf("checkpoint image index size %d is outside the allowed range", target.Size)
	}
	if err := target.Digest.Validate(); err != nil {
		return imagespec.Descriptor{}, fmt.Errorf("checkpoint image index has invalid digest: %w", err)
	}
	allowedAnnotations := map[string]struct{}{
		images.AnnotationImageName:  {},
		imagespec.AnnotationRefName: {},
	}
	for annotation := range target.Annotations {
		if _, ok := allowedAnnotations[annotation]; !ok {
			return imagespec.Descriptor{}, fmt.Errorf("checkpoint archive image contains unsupported annotation %q", annotation)
		}
	}
	// Names in the archive are intentionally discarded. The caller creates the
	// only image record under a deterministic, restore-scoped name.
	target.Annotations = nil
	return target, nil
}

func restoreContainerImageConfigDigest(info containers.Container, containerID string) (digest.Digest, error) {
	extension, ok := info.Extensions[crilabels.ContainerMetadataExtension]
	if !ok {
		return "", fmt.Errorf("restored container %q has no CRI metadata extension", containerID)
	}
	metadata := new(containerstore.Metadata)
	if err := json.Unmarshal(extension.GetValue(), metadata); err != nil {
		return "", fmt.Errorf("failed to decode CRI metadata for restored container %q: %w", containerID, err)
	}
	if metadata.ID != containerID {
		return "", fmt.Errorf("restored container metadata ID %q does not match container ID %q", metadata.ID, containerID)
	}
	imageRef := digest.Digest(metadata.ImageRef)
	if err := imageRef.Validate(); err != nil {
		return "", fmt.Errorf("restored container %q has invalid image config digest %q: %w", containerID, imageRef, err)
	}
	return imageRef, nil
}

func validateRestoreImageConfig(checkpoint, restore digest.Digest) error {
	if err := checkpoint.Validate(); err != nil {
		return fmt.Errorf("checkpoint base image config digest %q is invalid: %w", checkpoint, err)
	}
	if err := restore.Validate(); err != nil {
		return fmt.Errorf("restore base image config digest %q is invalid: %w", restore, err)
	}
	if restore != checkpoint {
		return fmt.Errorf("restore base image config digest %q does not match checkpoint digest %q: %w", restore, checkpoint, errdefs.ErrFailedPrecondition)
	}
	return nil
}

func validateCheckpointIndex(index *imagespec.Index) (*imagespec.Descriptor, *imagespec.Descriptor, error) {
	if index.SchemaVersion != 2 {
		return nil, nil, fmt.Errorf("checkpoint image index schema version %d is unsupported", index.SchemaVersion)
	}
	allowedAnnotations := map[string]struct{}{
		imagespec.AnnotationRefName: {},
		checkpointRuntimeNameLabel:  {},
		checkpointSnapshotterLabel:  {},
	}
	for annotation := range index.Annotations {
		if _, ok := allowedAnnotations[annotation]; !ok {
			return nil, nil, fmt.Errorf("checkpoint image index contains unsupported annotation %q", annotation)
		}
	}
	if index.Annotations[checkpointRuntimeNameLabel] == "" {
		return nil, nil, errors.New("checkpoint image index has no runtime annotation")
	}
	if index.Annotations[checkpointSnapshotterLabel] == "" {
		return nil, nil, errors.New("checkpoint image index has no snapshotter annotation")
	}

	expected := map[string]int{
		images.MediaTypeContainerd1Checkpoint:               1,
		images.MediaTypeContainerd1CheckpointConfig:         1,
		images.MediaTypeContainerd1CheckpointOptions:        1,
		images.MediaTypeContainerd1CheckpointRuntimeOptions: -1,
		imagespec.MediaTypeImageLayerGzip:                   1,
	}
	counts := make(map[string]int, len(expected))
	var taskCheckpoint *imagespec.Descriptor
	var rw *imagespec.Descriptor
	for i := range index.Manifests {
		desc := &index.Manifests[i]
		limit, ok := expected[desc.MediaType]
		if !ok {
			return nil, nil, fmt.Errorf("checkpoint image index contains unsupported descriptor media type %q", desc.MediaType)
		}
		counts[desc.MediaType]++
		if limit >= 0 && counts[desc.MediaType] > limit {
			return nil, nil, fmt.Errorf("checkpoint image index contains too many %q descriptors", desc.MediaType)
		}
		if limit < 0 && counts[desc.MediaType] > 1 {
			return nil, nil, fmt.Errorf("checkpoint image index contains too many %q descriptors", desc.MediaType)
		}
		descriptorLimit := int64(maxPodCheckpointManifestSize)
		if desc.MediaType == images.MediaTypeContainerd1Checkpoint || desc.MediaType == imagespec.MediaTypeImageLayerGzip {
			descriptorLimit = maxCheckpointArchiveSize
		}
		if desc.Size <= 0 || desc.Size > descriptorLimit {
			return nil, nil, fmt.Errorf("checkpoint descriptor %q has size %d outside the allowed range", desc.MediaType, desc.Size)
		}
		if err := desc.Digest.Validate(); err != nil {
			return nil, nil, fmt.Errorf("checkpoint descriptor %q has invalid digest: %w", desc.MediaType, err)
		}
		if desc.MediaType != imagespec.MediaTypeImageLayerGzip && len(desc.Annotations) != 0 {
			return nil, nil, fmt.Errorf("checkpoint descriptor %q contains forbidden annotations", desc.MediaType)
		}
		switch desc.MediaType {
		case images.MediaTypeContainerd1Checkpoint:
			taskCheckpoint = desc
		case imagespec.MediaTypeImageLayerGzip:
			rw = desc
		}
	}
	for mediaType, count := range expected {
		if count == 1 && counts[mediaType] != 1 {
			return nil, nil, fmt.Errorf("checkpoint image index must contain exactly one %q descriptor", mediaType)
		}
	}
	return taskCheckpoint, rw, nil
}

func validateCheckpointTaskContent(ctx context.Context, provider content.Provider, desc imagespec.Descriptor) error {
	reader, err := provider.ReaderAt(ctx, desc)
	if err != nil {
		return fmt.Errorf("failed to open task checkpoint content: %w", err)
	}
	defer reader.Close()
	if reader.Size() != desc.Size {
		return fmt.Errorf("task checkpoint content size %d does not match descriptor size %d", reader.Size(), desc.Size)
	}
	if err := validateCheckpointTar(content.NewReader(reader), desc.Size); err != nil {
		return fmt.Errorf("invalid task checkpoint content: %w", err)
	}
	return nil
}

func validateCheckpointDescriptorContent(ctx context.Context, provider content.Provider, desc imagespec.Descriptor) error {
	reader, err := provider.ReaderAt(ctx, desc)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint content %q: %w", desc.MediaType, err)
	}
	defer reader.Close()
	if reader.Size() != desc.Size {
		return fmt.Errorf("checkpoint content %q size %d does not match descriptor size %d", desc.MediaType, reader.Size(), desc.Size)
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
	dir, err := openCheckpointDirectory(checkpointDir)
	if err != nil {
		return podCheckpointManifest{}, err
	}
	defer dir.Close()
	return readPodCheckpointManifestAt(dir)
}

func readPodCheckpointManifestAt(checkpointDir *os.File) (podCheckpointManifest, error) {
	data, err := readBoundedRegularFileAt(checkpointDir, podCheckpointManifestFile, maxPodCheckpointManifestSize)
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
	if len(manifest.Containers) > maxPodCheckpointContainers {
		return podCheckpointManifest{}, fmt.Errorf("checkpoint manifest contains %d containers; maximum is %d", len(manifest.Containers), maxPodCheckpointContainers)
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

func decodeStrictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("document contains multiple JSON values")
}

func readPodCheckpointConfig(checkpointDir string) (*runtime.PodSandboxConfig, error) {
	dir, err := openCheckpointDirectory(checkpointDir)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return readPodCheckpointConfigAt(dir)
}

func readPodCheckpointConfigAt(checkpointDir *os.File) (*runtime.PodSandboxConfig, error) {
	data, err := readBoundedRegularFileAt(checkpointDir, podCheckpointConfigFile, maxPodCheckpointConfigSize)
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

func openCheckpointDirectory(checkpointDir string) (*os.File, error) {
	fd, err := unix.Open(checkpointDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("checkpoint path %q must be an existing real directory: %w", checkpointDir, err)
	}
	return os.NewFile(uintptr(fd), checkpointDir), nil
}

func openRegularFileAt(dir *os.File, name string, maxSize int64) (*os.File, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return nil, fmt.Errorf("invalid checkpoint file name %q", name)
	}
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), filepath.Join(dir.Name(), name))
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("checkpoint entry is not a regular file")
	}
	if info.Size() < 0 || info.Size() > maxSize {
		file.Close()
		return nil, fmt.Errorf("checkpoint entry size %d exceeds limit %d", info.Size(), maxSize)
	}
	return file, nil
}

func readBoundedRegularFileAt(dir *os.File, name string, maxSize int64) ([]byte, error) {
	file, err := openRegularFileAt(dir, name, maxSize)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("checkpoint entry exceeds limit %d", maxSize)
	}
	return data, nil
}

func closeRestoreContainers(containers []restoreContainer) error {
	var closeErrors []error
	for _, container := range containers {
		if container.archive != nil {
			if err := container.archive.Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close checkpoint archive for container %q: %w", container.name, err))
			}
		}
	}
	return errors.Join(closeErrors...)
}

func validateCheckpointTar(r io.Reader, maxSize int64) error {
	tr := tar.NewReader(r)
	var entries int
	var totalSize int64
	seenPaths := make(map[string]struct{})
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid tar stream: %w", err)
		}
		entries++
		if entries > maxCheckpointArchiveEntries {
			return fmt.Errorf("tar contains more than %d entries", maxCheckpointArchiveEntries)
		}
		if err := validateCheckpointTarPath(header.Name); err != nil {
			return err
		}
		canonicalPath := strings.TrimSuffix(header.Name, "/")
		if _, ok := seenPaths[canonicalPath]; ok {
			return fmt.Errorf("tar contains duplicate path %q", header.Name)
		}
		seenPaths[canonicalPath] = struct{}{}
		switch header.Typeflag {
		case tar.TypeReg:
			if header.Size < 0 || header.Size > maxSize-totalSize {
				return fmt.Errorf("tar content exceeds limit %d", maxSize)
			}
			totalSize += header.Size
		case tar.TypeDir:
			if header.Size != 0 {
				return fmt.Errorf("tar directory %q has non-zero size", header.Name)
			}
		default:
			return fmt.Errorf("tar entry %q has forbidden type %d", header.Name, header.Typeflag)
		}
	}
}

func validateCheckpointTarPath(name string) error {
	if name == "" || path.IsAbs(name) || strings.Contains(name, "\\") {
		return fmt.Errorf("tar contains invalid path %q", name)
	}
	trimmed := strings.TrimSuffix(name, "/")
	cleaned := path.Clean(trimmed)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != trimmed {
		return fmt.Errorf("tar contains non-canonical path %q", name)
	}
	return nil
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
