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

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	imagearchive "github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/containerd/v2/core/sandbox"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type restoreContainer struct {
	id       string
	name     string
	config   *runtime.ContainerConfig
	archive  *os.File
	imageRef digest.Digest
	manifest podCheckpointManifestContainer
}

// Restore prepares containers created by the caller for restore. On failure it
// removes the checkpoint images it imported, but writable layers already
// applied to container snapshots stay; the caller must remove the containers.
func (c *CheckpointService) Restore(ctx context.Context, sandboxID string, opts sandbox.RestoreOptions) (_ sandbox.RestoreResult, retErr error) {
	if sandboxID == "" {
		return sandbox.RestoreResult{}, fmt.Errorf("sandbox ID is required for restore: %w", errdefs.ErrInvalidArgument)
	}
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return sandbox.RestoreResult{}, err
	}
	if err := c.checkCriu(); err != nil {
		return sandbox.RestoreResult{}, err
	}
	if err := validatePodRestoreOptions(opts.Options); err != nil {
		return sandbox.RestoreResult{}, err
	}
	// Restored image names are derived from the sandbox and container IDs, so
	// a concurrent restore of the same sandbox could delete this one's images.
	key := podCheckpointKey(namespace, sandboxID)
	if _, loaded := c.podCheckpointsInProgress.LoadOrStore(key, struct{}{}); loaded {
		return sandbox.RestoreResult{}, fmt.Errorf("checkpoint or restore for pod sandbox %q is already in progress: %w", sandboxID, errdefs.ErrAlreadyExists)
	}
	defer c.podCheckpointsInProgress.Delete(key)
	containers, err := prepareRestoreContainers(opts)
	if err != nil {
		return sandbox.RestoreResult{}, invalidRestoreInput(err)
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
		cleanupCtx, cancel := podCheckpointCleanupContext(namespace)
		defer cancel()
		for _, image := range importedImages {
			if err := c.client.ImageService().Delete(cleanupCtx, image, images.SynchronousDelete()); err != nil && !errdefs.IsNotFound(err) {
				retErr = errors.Join(retErr, fmt.Errorf("failed to remove checkpoint image %q: %w", image, err))
			}
		}
	}()
	for _, container := range containers {
		if err := ctx.Err(); err != nil {
			return sandbox.RestoreResult{}, fmt.Errorf("pod restore aborted before preparing container %q: %w", container.name, err)
		}
		checkpointImage := restoreCheckpointImageName(sandboxID, container.id)
		if err := c.importContainerCheckpoint(ctx, sandboxID, container, checkpointImage); err != nil {
			return sandbox.RestoreResult{}, fmt.Errorf("failed to prepare restored container %q: %w", container.name, err)
		}
		importedImages = append(importedImages, checkpointImage)
		result.RestoredContainers = append(result.RestoredContainers, sandbox.RestoredContainer{
			ID:                  container.id,
			Name:                container.name,
			TaskCheckpointImage: checkpointImage,
		})
	}
	return result, nil
}

// invalidRestoreInput classifies an error from validating restore input as an
// invalid argument unless it already carries an error class.
func invalidRestoreInput(err error) error {
	if !errdefs.IsUnknown(errdefs.Resolve(err)) {
		return err
	}
	return fmt.Errorf("%w: %w", err, errdefs.ErrInvalidArgument)
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
		if err := decodeCRIJSON(manifestContainer.Config, checkpointConfig); err != nil {
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
		if err := decodeCRIJSON(manifestContainer.Status, checkpointStatus); err != nil {
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

func (c *CheckpointService) importContainerCheckpoint(ctx context.Context, sandboxID string, container restoreContainer, imageName string) (retErr error) {
	createdContainer, err := c.client.LoadContainer(ctx, container.id)
	if err != nil {
		return fmt.Errorf("failed to load restored container %q: %w", container.id, err)
	}
	info, err := createdContainer.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to inspect restored container %q: %w", container.id, err)
	}
	// The checkpoint's writable layer is applied to this container's snapshot,
	// so it must be a new container of the restored sandbox.
	if info.SandboxID != sandboxID {
		return fmt.Errorf("restored container %q belongs to sandbox %q, not %q: %w", container.id, info.SandboxID, sandboxID, errdefs.ErrInvalidArgument)
	}
	if _, err := createdContainer.Task(ctx, nil); err == nil {
		return fmt.Errorf("restored container %q already has a task: %w", container.id, errdefs.ErrFailedPrecondition)
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to check task of restored container %q: %w", container.id, err)
	}
	restoreImageRef, err := restoreContainerImageConfigDigest(info, container.id)
	if err != nil {
		return err
	}
	if err := validateRestoreImageConfig(container.imageRef, restoreImageRef); err != nil {
		return err
	}

	if _, err := container.archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind checkpoint archive: %w", err)
	}
	if err := c.client.ImageService().Delete(ctx, imageName, images.SynchronousDelete()); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to replace checkpoint image %q: %w", imageName, err)
	}
	defer func() {
		if retErr != nil {
			namespace, _ := namespaces.Namespace(ctx)
			cleanupCtx, cancel := podCheckpointCleanupContext(namespace)
			defer cancel()
			if err := c.client.ImageService().Delete(cleanupCtx, imageName, images.SynchronousDelete()); err != nil && !errdefs.IsNotFound(err) {
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
	rw, err := validateCheckpointIndex(&index)
	if err != nil {
		return err
	}
	for _, desc := range index.Manifests {
		if err := validateCheckpointDescriptorContent(ctx, c.client.ContentStore(), desc); err != nil {
			return err
		}
	}
	if checkpointRuntime := index.Annotations[checkpointRuntimeNameLabel]; checkpointRuntime != "" && checkpointRuntime != info.Runtime.Name {
		return fmt.Errorf("checkpoint runtime %q does not match restore runtime %q: %w", checkpointRuntime, info.Runtime.Name, errdefs.ErrFailedPrecondition)
	}
	// ImportIndex does not set GC references. Keep the validated content reachable
	// through the checkpoint image after the import lease is released.
	setChildrenLabels := images.SetChildrenLabels(c.client.ContentStore(), func(context.Context, imagespec.Descriptor) ([]imagespec.Descriptor, error) {
		return index.Manifests, nil
	})
	if _, err := setChildrenLabels(leaseCtx, checkpoint.Target()); err != nil {
		return fmt.Errorf("failed to retain checkpoint image content: %w", err)
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

// validateCheckpointIndex returns the writable-layer descriptor.
func validateCheckpointIndex(index *imagespec.Index) (*imagespec.Descriptor, error) {
	if index.SchemaVersion != 2 {
		return nil, fmt.Errorf("checkpoint image index schema version %d is unsupported", index.SchemaVersion)
	}
	allowedAnnotations := map[string]struct{}{
		imagespec.AnnotationRefName: {},
		checkpointRuntimeNameLabel:  {},
		checkpointSnapshotterLabel:  {},
	}
	for annotation := range index.Annotations {
		if _, ok := allowedAnnotations[annotation]; !ok {
			return nil, fmt.Errorf("checkpoint image index contains unsupported annotation %q", annotation)
		}
	}
	if index.Annotations[checkpointRuntimeNameLabel] == "" {
		return nil, errors.New("checkpoint image index has no runtime annotation")
	}
	if index.Annotations[checkpointSnapshotterLabel] == "" {
		return nil, errors.New("checkpoint image index has no snapshotter annotation")
	}

	// Every descriptor type appears at most once; required ones exactly once.
	required := map[string]bool{
		images.MediaTypeContainerd1Checkpoint:               true,
		images.MediaTypeContainerd1CheckpointConfig:         true,
		images.MediaTypeContainerd1CheckpointOptions:        true,
		images.MediaTypeContainerd1CheckpointRuntimeOptions: false,
		imagespec.MediaTypeImageLayerGzip:                   true,
	}
	counts := make(map[string]int, len(required))
	var rw *imagespec.Descriptor
	for i := range index.Manifests {
		desc := &index.Manifests[i]
		if _, ok := required[desc.MediaType]; !ok {
			return nil, fmt.Errorf("checkpoint image index contains unsupported descriptor media type %q", desc.MediaType)
		}
		counts[desc.MediaType]++
		if counts[desc.MediaType] > 1 {
			return nil, fmt.Errorf("checkpoint image index contains too many %q descriptors", desc.MediaType)
		}
		descriptorLimit := int64(maxPodCheckpointManifestSize)
		if desc.MediaType == images.MediaTypeContainerd1Checkpoint || desc.MediaType == imagespec.MediaTypeImageLayerGzip {
			descriptorLimit = maxCheckpointArchiveSize
		}
		if desc.Size <= 0 || desc.Size > descriptorLimit {
			return nil, fmt.Errorf("checkpoint descriptor %q has size %d outside the allowed range", desc.MediaType, desc.Size)
		}
		if err := desc.Digest.Validate(); err != nil {
			return nil, fmt.Errorf("checkpoint descriptor %q has invalid digest: %w", desc.MediaType, err)
		}
		if err := validateCheckpointDescriptorAnnotations(desc); err != nil {
			return nil, err
		}
		if desc.MediaType == imagespec.MediaTypeImageLayerGzip {
			rw = desc
		}
	}
	for mediaType, isRequired := range required {
		if isRequired && counts[mediaType] != 1 {
			return nil, fmt.Errorf("checkpoint image index must contain exactly one %q descriptor", mediaType)
		}
	}
	return rw, nil
}

// validateCheckpointDescriptorAnnotations rejects annotations that a consumer
// could interpret as control input, such as RestoreFromPath on the task
// checkpoint. Only the writable layer may record its uncompressed digest.
func validateCheckpointDescriptorAnnotations(desc *imagespec.Descriptor) error {
	for annotation := range desc.Annotations {
		if desc.MediaType == imagespec.MediaTypeImageLayerGzip && annotation == labels.LabelUncompressed {
			continue
		}
		return fmt.Errorf("checkpoint descriptor %q contains forbidden annotations", desc.MediaType)
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

func restoreCheckpointImageName(sandboxID, containerID string) string {
	return fmt.Sprintf("localhost/containerd-pod-restore:%x", sha256.Sum256([]byte(sandboxID+"\x00"+containerID)))
}

func readPodCheckpointManifestAt(checkpointDir *os.File) (podCheckpointManifest, error) {
	data, err := readBoundedRegularFileAt(checkpointDir, podCheckpointManifestFile, maxPodCheckpointManifestSize)
	if err != nil {
		return podCheckpointManifest{}, fmt.Errorf("failed to read checkpoint manifest: %w", err)
	}
	var manifest podCheckpointManifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
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

// decodeCRIJSON decodes a CRI message saved by Checkpoint. Unlike the
// checkpoint's own versioned metadata, these messages follow the CRI API
// version containerd was built with. Ignore fields added by a newer CRI API:
// this containerd cannot apply them to the destination containers either.
func decodeCRIJSON(data []byte, value any) error {
	return json.Unmarshal(data, value)
}

func readPodCheckpointConfigAt(checkpointDir *os.File) (*runtime.PodSandboxConfig, error) {
	data, err := readBoundedRegularFileAt(checkpointDir, podCheckpointConfigFile, maxPodCheckpointConfigSize)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint sandbox config: %w", err)
	}
	config := new(runtime.PodSandboxConfig)
	if err := decodeCRIJSON(data, config); err != nil {
		return nil, fmt.Errorf("failed to decode checkpoint sandbox config: %w", err)
	}
	return config, nil
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
	// Open without blocking on FIFOs so the regular-file check can reject them.
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
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

func validateRestoreSandboxConfig(checkpoint, restore *runtime.PodSandboxConfig) error {
	// The destination Pod may have a new name and therefore a new default
	// hostname. RunPodSandbox applies it to the destination UTS namespace;
	// process-owned state, including existing environment variables, stays saved.
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
	// The kubelet pins tagged source images to repository digests on restore.
	// Compare the resolved image config digests in importContainerCheckpoint;
	// the original user-supplied image strings need not be identical.
	// Downward API values and command/argument expansion describe the new Pod.
	// CRIU resumes the captured process with its original argv and environment;
	// these request fields cannot change that state or launch a new entrypoint.
	if checkpoint.GetWorkingDir() != restore.GetWorkingDir() {
		return fmt.Errorf("restore working directory %q does not match checkpoint working directory %q: %w", restore.GetWorkingDir(), checkpoint.GetWorkingDir(), errdefs.ErrFailedPrecondition)
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

func validatePodRestoreOptions(options map[string]string) error {
	return rejectUnsupportedPodOptions("pod restore", options)
}
