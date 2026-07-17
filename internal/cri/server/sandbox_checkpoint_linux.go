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

package server

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
	"sort"
	"time"

	"github.com/containerd/containerd/v2/client"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	podCheckpointManifestVersion = 1
	podCheckpointManifestFile    = "checkpoint-manifest.json"
	podCheckpointConfigFile      = "pod-config.json"
)

type podCheckpointManifest struct {
	Version    int                              `json:"version"`
	SandboxID  string                           `json:"sandboxId"`
	Containers []podCheckpointManifestContainer `json:"containers"`
}

type podCheckpointManifestContainer struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Archive string `json:"archive"`
}

type checkpointContainer struct {
	container containerstore.Container
	manifest  podCheckpointManifestContainer
	task      client.Task
}

// CheckpointPod creates a Pod-level checkpoint of the caller-selected
// containers in the pod sandbox into a caller-owned directory.
func (c *criService) CheckpointPod(ctx context.Context, r *runtime.CheckpointPodRequest) (_ *runtime.CheckpointPodResponse, retErr error) {
	if r == nil {
		return nil, errors.New("checkpoint request is required")
	}
	if err := requireRequestDeadline(ctx, "pod checkpoint"); err != nil {
		return nil, err
	}
	if err := validatePodCheckpointOptions(r.GetOptions()); err != nil {
		return nil, err
	}
	requestedSandboxID := r.GetPodSandboxId()
	if requestedSandboxID == "" {
		return nil, errors.New("pod sandbox ID is required")
	}
	log.G(ctx).WithField("podSandboxId", requestedSandboxID).Debug("CheckpointPod")

	checkpointDir := r.GetOutputPath()
	releaseOutput, err := c.reservePodCheckpointOutput(checkpointDir)
	if err != nil {
		return nil, err
	}
	defer releaseOutput()

	sandbox, err := c.sandboxStore.Get(requestedSandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox %q: %w", requestedSandboxID, err)
	}
	sandboxID := sandbox.ID
	if _, loaded := c.podCheckpointsInProgress.LoadOrStore(sandboxID, struct{}{}); loaded {
		return nil, fmt.Errorf("checkpoint for pod sandbox %q is already in progress", sandboxID)
	}
	defer c.podCheckpointsInProgress.Delete(sandboxID)

	if state := sandbox.Status.Get().State; state != sandboxstore.StateReady {
		return nil, fmt.Errorf("pod sandbox %q must be running, found state %s", sandboxID, state)
	}
	containers, err := c.selectCheckpointContainers(sandboxID, r.GetContainerIds())
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pod checkpoint aborted before writing output: %w", err)
	}
	containerIDs := make([]string, 0, len(containers))
	for _, selected := range containers {
		containerIDs = append(containerIDs, selected.container.ID)
	}
	releaseContainers, err := c.reserveContainerCheckpoints(containerIDs)
	if err != nil {
		return nil, err
	}
	defer releaseContainers()
	if err := resolveCheckpointTasks(ctx, containers); err != nil {
		return nil, err
	}

	freezer, err := loadPodCgroupFreezer(sandbox.Config.GetLinux().GetCgroupParent())
	if err != nil {
		return nil, err
	}
	state, err := freezer.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect pod cgroup before checkpoint: %w", err)
	}
	if state != podCgroupStateThawed {
		return nil, fmt.Errorf("refusing to checkpoint a pod whose cgroup is %s", state)
	}

	completed := false
	defer func() {
		if completed {
			return
		}
		if err := cleanupPartialCheckpoint(checkpointDir); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to clean partial checkpoint output %q: %w", checkpointDir, err))
		}
	}()

	configData, err := json.Marshal(sandbox.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal sandbox config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(checkpointDir, podCheckpointConfigFile), configData, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write sandbox config: %w", err)
	}

	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		SandboxID:    sandboxID,
		CgroupParent: sandbox.Config.GetLinux().GetCgroupParent(),
		ContainerIDs: containerIDs,
	}
	tasks := make([]podCheckpointRuntimeTask, 0, len(containers))
	for _, selected := range containers {
		tasks = append(tasks, selected.task)
	}
	markerActive, markerErr := c.writePodCheckpointMarker(marker)
	defer func() {
		if !markerActive {
			return
		}
		cleanupCtx, cancel := util.DeferContext()
		defer cancel()
		if err := recoverPodCheckpointTasks(cleanupCtx, freezer, tasks); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to recover pod after checkpoint: %w", err))
			return
		}
		if err := c.removePodCheckpointMarker(sandboxID); err != nil {
			retErr = errors.Join(retErr, err)
			return
		}
		markerActive = false
	}()
	if markerErr != nil {
		return nil, markerErr
	}

	if err := pausePodCheckpointTasks(ctx, freezer, tasks); err != nil {
		return nil, err
	}

	manifest := podCheckpointManifest{
		Version:    podCheckpointManifestVersion,
		SandboxID:  sandboxID,
		Containers: make([]podCheckpointManifestContainer, 0, len(containers)),
	}
	for _, selected := range containers {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("pod checkpoint aborted: %w", err)
		}

		containerCheckpointPath := filepath.Join(checkpointDir, selected.manifest.Archive)
		if _, err := c.checkpointContainerLocked(ctx, &runtime.CheckpointContainerRequest{
			ContainerId: selected.container.ID,
			Location:    containerCheckpointPath,
		}, selected.container, selected.task, time.Now()); err != nil {
			return nil, fmt.Errorf("failed to checkpoint container %q: %w", selected.manifest.Name, err)
		}
		archiveInfo, err := os.Lstat(containerCheckpointPath)
		if err != nil {
			return nil, fmt.Errorf("checkpoint archive for container %q is not accessible after checkpoint: %w", selected.manifest.Name, err)
		}
		if !archiveInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("checkpoint archive for container %q is not a regular file after checkpoint", selected.manifest.Name)
		}
		manifest.Containers = append(manifest.Containers, selected.manifest)
		log.G(ctx).WithField("container", selected.manifest.Name).Debug("Container checkpointed")
	}

	cleanupCtx, cancel := util.DeferContext()
	if err := recoverPodCheckpointTasks(cleanupCtx, freezer, tasks); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to resume pod after checkpoint: %w", err)
	}
	cancel()
	if err := c.removePodCheckpointMarker(sandboxID); err != nil {
		return nil, err
	}
	markerActive = false
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pod checkpoint aborted before writing manifest: %w", err)
	}

	// The manifest is the completion marker, so write it only after every
	// container checkpoint has succeeded.
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal checkpoint manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(checkpointDir, podCheckpointManifestFile), manifestData, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write checkpoint manifest: %w", err)
	}

	completed = true
	log.G(ctx).WithField("containers", len(manifest.Containers)).Info("Pod checkpoint completed")
	return &runtime.CheckpointPodResponse{}, nil
}

func (c *criService) selectCheckpointContainers(sandboxID string, containerIDs []string) ([]checkpointContainer, error) {
	if len(containerIDs) == 0 {
		return nil, errors.New("at least one container ID is required for pod checkpoint")
	}

	seenRequestedIDs := make(map[string]struct{}, len(containerIDs))
	seenContainerIDs := make(map[string]struct{}, len(containerIDs))
	seenContainerNames := make(map[string]struct{}, len(containerIDs))
	containers := make([]checkpointContainer, 0, len(containerIDs))
	for i, requestedID := range containerIDs {
		if requestedID == "" {
			return nil, fmt.Errorf("container ID at index %d is empty", i)
		}
		if _, exists := seenRequestedIDs[requestedID]; exists {
			return nil, fmt.Errorf("container ID %q is duplicated in checkpoint request", requestedID)
		}
		seenRequestedIDs[requestedID] = struct{}{}

		cntr, err := c.containerStore.Get(requestedID)
		if err != nil {
			return nil, fmt.Errorf("failed to find checkpoint container %q: %w", requestedID, err)
		}
		if _, exists := seenContainerIDs[cntr.ID]; exists {
			return nil, fmt.Errorf("container ID %q resolves to a duplicate checkpoint container %q", requestedID, cntr.ID)
		}
		seenContainerIDs[cntr.ID] = struct{}{}
		if cntr.SandboxID != sandboxID {
			return nil, fmt.Errorf("checkpoint container %q belongs to sandbox %q, not requested sandbox %q", cntr.ID, cntr.SandboxID, sandboxID)
		}
		if state := cntr.Status.Get().State(); state != runtime.ContainerState_CONTAINER_RUNNING {
			return nil, fmt.Errorf("checkpoint container %q must be running, found state %s", cntr.ID, criContainerStateToString(state))
		}

		containerName := cntr.Config.GetMetadata().GetName()
		if containerName == "" {
			return nil, fmt.Errorf("checkpoint container %q has no CRI metadata name", cntr.ID)
		}
		if _, exists := seenContainerNames[containerName]; exists {
			return nil, fmt.Errorf("checkpoint container name %q is duplicated", containerName)
		}
		seenContainerNames[containerName] = struct{}{}

		containers = append(containers, checkpointContainer{
			container: cntr,
			manifest: podCheckpointManifestContainer{
				Name:    containerName,
				ID:      cntr.ID,
				Archive: checkpointArchiveName(cntr.ID),
			},
		})
	}
	return containers, nil
}

func resolveCheckpointTasks(ctx context.Context, containers []checkpointContainer) error {
	for i := range containers {
		task, err := containers[i].container.Container.Task(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to load checkpoint task for container %q: %w", containers[i].container.ID, err)
		}
		status, err := task.Status(ctx)
		if err != nil {
			return fmt.Errorf("failed to inspect checkpoint task for container %q: %w", containers[i].container.ID, err)
		}
		if status.Status != client.Running {
			return fmt.Errorf("checkpoint task for container %q must be running, found state %s", containers[i].container.ID, status.Status)
		}
		containers[i].task = task
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
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("checkpoint output path %q must not be a symbolic link", checkpointDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("checkpoint output path %q is not a directory", checkpointDir)
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

func (c *criService) reservePodCheckpointOutput(checkpointDir string) (func(), error) {
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

// cleanupPartialCheckpoint removes the contents of a partially written pod
// checkpoint directory. The caller owns the directory itself.
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

type restoreContainer struct {
	checkpointName string
	checkpointPath string
	config         *runtime.ContainerConfig
}

type podRestoreContainerContextKey struct{}

func withPodRestoreContainerContext(ctx context.Context) context.Context {
	// CreateContainer also handles standalone checkpoint imports. The private
	// marker lets that shared path avoid replaying checkpoint metadata for a Pod
	// restore, where the complete CRI request is authoritative.
	return context.WithValue(ctx, podRestoreContainerContextKey{}, true)
}

func isPodRestoreContainerContext(ctx context.Context) bool {
	restore, _ := ctx.Value(podRestoreContainerContextKey{}).(bool)
	return restore
}

type podRestoreOperations interface {
	RunPodSandbox(context.Context, *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error)
	CreateContainer(context.Context, *runtime.CreateContainerRequest) (*runtime.CreateContainerResponse, error)
	RemovePodSandbox(context.Context, *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error)
}

func prepareRestoreRequest(r *runtime.RestorePodRequest) ([]restoreContainer, error) {
	if r == nil {
		return nil, errors.New("restore request is required")
	}
	checkpointDir := r.GetCheckpointPath()
	if checkpointDir == "" {
		return nil, errors.New("checkpoint path is required")
	}
	if !filepath.IsAbs(checkpointDir) {
		return nil, fmt.Errorf("checkpoint path %q must be absolute", checkpointDir)
	}
	info, err := os.Lstat(checkpointDir)
	if err != nil {
		return nil, fmt.Errorf("checkpoint path %q must be an existing directory: %w", checkpointDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("checkpoint path %q must not be a symbolic link", checkpointDir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("checkpoint path %q is not a directory", checkpointDir)
	}
	if r.Config == nil {
		return nil, errors.New("pod sandbox config is required for restore")
	}

	manifestPath := filepath.Join(checkpointDir, podCheckpointManifestFile)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect checkpoint manifest: %w", err)
	}
	if !manifestInfo.Mode().IsRegular() {
		return nil, errors.New("checkpoint manifest is not a regular file")
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint manifest: %w", err)
	}
	manifest, err := decodePodCheckpointManifest(manifestData)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint manifest: %w", err)
	}
	if manifest.Version != podCheckpointManifestVersion {
		return nil, fmt.Errorf("checkpoint manifest version %d is unsupported; expected %d", manifest.Version, podCheckpointManifestVersion)
	}
	if manifest.SandboxID == "" {
		return nil, errors.New("checkpoint manifest has no sandbox ID")
	}
	if len(manifest.Containers) == 0 {
		return nil, errors.New("checkpoint manifest contains no containers")
	}
	checkpointConfig, err := readPodCheckpointConfig(checkpointDir)
	if err != nil {
		return nil, err
	}
	if err := validateRestoreSandboxConfig(checkpointConfig, r.Config); err != nil {
		return nil, err
	}

	configsByName := make(map[string]*runtime.ContainerConfig, len(r.ContainerConfigs))
	for i, config := range r.ContainerConfigs {
		name := config.GetMetadata().GetName()
		if name == "" {
			return nil, fmt.Errorf("container config at index %d has no metadata name", i)
		}
		if _, exists := configsByName[name]; exists {
			return nil, fmt.Errorf("container config name %q is duplicated", name)
		}
		configsByName[name] = config
	}
	seenContainerIDs := make(map[string]struct{}, len(manifest.Containers))
	seenContainerNames := make(map[string]struct{}, len(manifest.Containers))
	seenArchives := make(map[string]struct{}, len(manifest.Containers))
	containers := make([]restoreContainer, 0, len(manifest.Containers))
	for i, checkpointContainer := range manifest.Containers {
		if checkpointContainer.Name == "" {
			return nil, fmt.Errorf("checkpoint manifest container at index %d has no name", i)
		}
		if _, exists := seenContainerNames[checkpointContainer.Name]; exists {
			return nil, fmt.Errorf("checkpoint manifest contains duplicate container name %q", checkpointContainer.Name)
		}
		seenContainerNames[checkpointContainer.Name] = struct{}{}
		if checkpointContainer.ID == "" {
			return nil, fmt.Errorf("checkpoint manifest container %q has no ID", checkpointContainer.Name)
		}
		if _, exists := seenContainerIDs[checkpointContainer.ID]; exists {
			return nil, fmt.Errorf("checkpoint manifest contains duplicate container ID %q", checkpointContainer.ID)
		}
		seenContainerIDs[checkpointContainer.ID] = struct{}{}

		expectedArchive := checkpointArchiveName(checkpointContainer.ID)
		if checkpointContainer.Archive != expectedArchive {
			return nil, fmt.Errorf("checkpoint manifest container %q has invalid archive name %q; expected %q", checkpointContainer.Name, checkpointContainer.Archive, expectedArchive)
		}
		if _, exists := seenArchives[checkpointContainer.Archive]; exists {
			return nil, fmt.Errorf("checkpoint manifest contains duplicate archive %q", checkpointContainer.Archive)
		}
		seenArchives[checkpointContainer.Archive] = struct{}{}

		archivePath := filepath.Join(checkpointDir, checkpointContainer.Archive)
		archiveInfo, err := os.Lstat(archivePath)
		if err != nil {
			return nil, fmt.Errorf("checkpoint archive for container %q is not accessible: %w", checkpointContainer.Name, err)
		}
		if !archiveInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("checkpoint archive for container %q is not a regular file", checkpointContainer.Name)
		}

		config, ok := configsByName[checkpointContainer.Name]
		if !ok {
			return nil, fmt.Errorf("checkpoint container %q has no matching container config", checkpointContainer.Name)
		}
		if config.GetImage().GetImage() == "" {
			return nil, fmt.Errorf("container config %q has no image", checkpointContainer.Name)
		}
		if config.GetLogPath() == "" {
			return nil, fmt.Errorf("container config %q has no log path", checkpointContainer.Name)
		}
		config = proto.Clone(config).(*runtime.ContainerConfig)
		if config.Image == nil {
			config.Image = &runtime.ImageSpec{}
		}
		config.Image.Image = archivePath
		containers = append(containers, restoreContainer{
			checkpointName: checkpointContainer.Name,
			checkpointPath: archivePath,
			config:         config,
		})
	}
	if len(configsByName) != len(manifest.Containers) {
		return nil, fmt.Errorf("restore request has %d container configs, but checkpoint manifest has %d container entries", len(configsByName), len(manifest.Containers))
	}
	return containers, nil
}

// RestorePod restores a Pod sandbox and its containers from a checkpoint.
func (c *criService) RestorePod(ctx context.Context, r *runtime.RestorePodRequest) (_ *runtime.RestorePodResponse, retErr error) {
	if err := requireRequestDeadline(ctx, "pod restore"); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.New("restore request is required")
	}
	if err := validatePodRestoreOptions(r.GetOptions()); err != nil {
		return nil, err
	}
	containers, err := prepareRestoreRequest(r)
	if err != nil {
		return nil, err
	}
	return restorePod(ctx, r, containers, c)
}

// Keep the two validators separate because checkpoint and restore option
// namespaces evolve independently. Request options are operation-local and
// must not be copied into the checkpoint manifest for later replay.
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
	return fmt.Errorf("%s options %q are not supported by containerd: %w", operation, keys, errdefs.ErrInvalidArgument)
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
	configPath := filepath.Join(checkpointDir, podCheckpointConfigFile)
	info, err := os.Lstat(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect checkpoint sandbox config: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("checkpoint sandbox config is not a regular file")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint sandbox config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	config := new(runtime.PodSandboxConfig)
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint sandbox config: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return config, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint sandbox config: %w", err)
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

func restorePod(ctx context.Context, r *runtime.RestorePodRequest, containers []restoreContainer, operations podRestoreOperations) (_ *runtime.RestorePodResponse, retErr error) {
	log.G(ctx).WithField("checkpointPath", r.GetCheckpointPath()).Debug("RestorePod")
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pod restore aborted before sandbox creation: %w", err)
	}
	requestedNames := make(map[string]struct{}, len(containers))
	for i, container := range containers {
		name := container.config.GetMetadata().GetName()
		if name == "" {
			return nil, fmt.Errorf("restored container at index %d has no metadata name", i)
		}
		if name != container.checkpointName {
			return nil, fmt.Errorf("restored container config name %q does not match checkpoint container name %q", name, container.checkpointName)
		}
		if _, exists := requestedNames[name]; exists {
			return nil, fmt.Errorf("restored container name %q is duplicated", name)
		}
		requestedNames[name] = struct{}{}
	}

	runResp, err := operations.RunPodSandbox(ctx, &runtime.RunPodSandboxRequest{
		Config:         r.Config,
		RuntimeHandler: r.GetRuntimeHandler(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox for restore: %w", err)
	}
	sandboxID := runResp.GetPodSandboxId()
	if sandboxID == "" {
		return nil, errors.New("failed to create sandbox for restore: runtime returned an empty sandbox ID")
	}
	rollback := true
	defer func() {
		if !rollback {
			return
		}
		cleanupCtx, cancel := util.DeferContext()
		defer cancel()
		_, err := operations.RemovePodSandbox(cleanupCtx, &runtime.RemovePodSandboxRequest{PodSandboxId: sandboxID})
		if err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to roll back restored sandbox %q: %w", sandboxID, err))
		}
	}()

	restoredContainers := make([]*runtime.RestoredContainer, 0, len(containers))
	seenContainerIDs := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("pod restore aborted before creating container %q: %w", container.checkpointName, err)
		}
		createResp, err := operations.CreateContainer(withPodRestoreContainerContext(ctx), &runtime.CreateContainerRequest{
			PodSandboxId:  sandboxID,
			Config:        container.config,
			SandboxConfig: r.Config,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create restored container %q: %w", container.checkpointName, err)
		}
		containerID := createResp.GetContainerId()
		if containerID == "" {
			return nil, fmt.Errorf("failed to create restored container %q: runtime returned an empty container ID", container.checkpointName)
		}
		if _, exists := seenContainerIDs[containerID]; exists {
			return nil, fmt.Errorf("failed to create restored container %q: runtime returned duplicate container ID %q", container.checkpointName, containerID)
		}
		seenContainerIDs[containerID] = struct{}{}
		restoredContainers = append(restoredContainers, &runtime.RestoredContainer{
			Name:        container.config.GetMetadata().GetName(),
			ContainerId: containerID,
		})
		log.G(ctx).
			WithField("container", container.checkpointName).
			WithField("checkpointPath", container.checkpointPath).
			Info("Restored container created from checkpoint")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pod restore aborted after creating restored containers: %w", err)
	}

	rollback = false
	log.G(ctx).WithField("sandboxId", sandboxID).Info("Pod restore completed")
	return &runtime.RestorePodResponse{
		PodSandboxId:       sandboxID,
		RestoredContainers: restoredContainers,
	}, nil
}
