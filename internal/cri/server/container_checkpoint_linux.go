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
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	crmetadata "github.com/checkpoint-restore/checkpointctl/lib"
	"github.com/checkpoint-restore/go-criu/v7/stats"
	"github.com/checkpoint-restore/go-criu/v7/utils"
	"github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/cri/annotations"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"

	"github.com/distribution/reference"
	"github.com/opencontainers/image-spec/identity"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	spec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	kubetypes "k8s.io/kubelet/pkg/types"
)

// checkIfCheckpointOCIImage returns checks if the input refers to a checkpoint image.
// It returns the StorageImageID of the image the input resolves to, nil otherwise.
func (c *criService) checkIfCheckpointOCIImage(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", nil
	}
	if _, err := os.Stat(input); err == nil {
		return "", nil
	}

	image, err := c.LocalResolve(input)
	if err != nil {
		return "", fmt.Errorf("failed to resolve image %q: %w", input, err)
	}
	containerdImage, err := c.toContainerdImage(ctx, image)
	if err != nil {
		return "", fmt.Errorf("failed to get image from containerd %q: %w", input, err)
	}
	input = containerdImage.Name()
	images, err := c.client.ImageService().Get(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to get image from containerd %q: %w", input, err)
	}

	rawIndex, err := content.ReadBlob(ctx, c.client.ContentStore(), images.Target)
	if err != nil {
		return "", fmt.Errorf("failed to read image blob from containerd %q: %w", input, err)
	}

	var index v1.Index
	if err = json.Unmarshal(rawIndex, &index); err != nil {
		return "", fmt.Errorf("failed to unmarshall blob into OCI index: %w", err)
	}

	if index.Annotations == nil {
		return "", nil
	}

	ann, ok := index.Annotations[crmetadata.CheckpointAnnotationName]
	if !ok {
		return "", nil
	}

	log.G(ctx).Infof("Found checkpoint of container %v in %v", ann, input)

	return image.ID, nil
}

func (c *criService) CRImportCheckpoint(
	ctx context.Context,
	meta *containerstore.Metadata,
	sandbox *sandbox.Sandbox,
	sandboxConfig *runtime.PodSandboxConfig,
) (ctrID string, retErr error) {
	var mountPoint string
	start := time.Now()
	// Ensure that the image to restore the checkpoint from has been provided.
	if meta.Config.Image == nil || meta.Config.Image.Image == "" {
		return "", errors.New(`attribute "image" missing from container definition`)
	}

	inputImage := meta.Config.Image.Image
	createAnnotations := meta.Config.Annotations
	createLabels := meta.Config.Labels

	restoreStorageImageID, err := c.checkIfCheckpointOCIImage(ctx, inputImage)
	if err != nil {
		return "", err
	}

	mountPoint, err = os.MkdirTemp("", "checkpoint")
	if err != nil {
		return "", err
	}
	defer func() {
		if err := os.RemoveAll(mountPoint); err != nil {
			log.G(ctx).Errorf("Could not recursively remove %s: %q", mountPoint, err)
		}
	}()
	var archiveFile *os.File
	if restoreStorageImageID != "" {
		log.G(ctx).Debugf("Restoring from oci image %s", inputImage)
		platform, err := c.sandboxService.SandboxPlatform(ctx, sandbox.Sandboxer, sandbox.ID)
		if err != nil {
			return "", fmt.Errorf("failed to query sandbox platform: %w", err)
		}
		img, err := c.client.ImageService().Get(ctx, restoreStorageImageID)
		if err != nil {
			return "", err
		}

		i := client.NewImageWithPlatform(c.client, img, platforms.Only(platform))
		diffIDs, err := i.RootFS(ctx)
		if err != nil {
			return "", err
		}
		chainID := identity.ChainID(diffIDs).String()
		ociRuntime, err := c.config.GetSandboxRuntime(sandboxConfig, sandbox.Metadata.RuntimeHandler)
		if err != nil {
			return "", fmt.Errorf("failed to get sandbox runtime: %w", err)
		}
		s := c.client.SnapshotService(c.RuntimeSnapshotter(ctx, ociRuntime))

		mounts, err := s.View(ctx, mountPoint, chainID)
		if err != nil {
			if errdefs.IsAlreadyExists(err) {
				mounts, err = s.Mounts(ctx, mountPoint)
			}
			if err != nil {
				return "", err
			}
		}
		if err := mount.All(mounts, mountPoint); err != nil {
			return "", err
		}
	} else {

		archiveFile, err = os.Open(inputImage)
		if err != nil {
			return "", fmt.Errorf("failed to open checkpoint archive %s for import: %w", inputImage, err)
		}
		defer func(f *os.File) {
			if err := f.Close(); err != nil {
				log.G(ctx).Errorf("Unable to close file %s: %q", f.Name(), err)
			}
		}(archiveFile)

		filter := archive.WithFilter(func(hdr *tar.Header) (bool, error) {
			// The checkpoint archive is unpacked twice if using a tar file directly.
			// The first time only the metadata files are relevant to prepare the
			// restore operation. This filter function ignores the large parts of
			// the checkpoint archive. This is usually the actual checkpoint
			// coming from CRIU as well as the rootfs diff tar file.
			excludePatterns := []string{
				"artifacts",
				"ctr.log",
				crmetadata.RootFsDiffTar,
				crmetadata.NetworkStatusFile,
				crmetadata.DeletedFilesFile,
				crmetadata.CheckpointDirectory,
			}
			for _, pattern := range excludePatterns {
				if strings.HasPrefix(hdr.Name, pattern) {
					return false, nil
				}
			}
			return true, nil
		})

		_, err = archive.Apply(
			ctx,
			mountPoint,
			archiveFile,
			[]archive.ApplyOpt{filter}...,
		)

		if err != nil {
			return "", fmt.Errorf("unpacking of checkpoint archive %s failed: %w", mountPoint, err)
		}
		log.G(ctx).Debugf("Unpacked checkpoint in %s", mountPoint)
	}
	// Load spec.dump from temporary directory
	dumpSpec := new(spec.Spec)
	if _, err := crmetadata.ReadJSONFile(dumpSpec, mountPoint, crmetadata.SpecDumpFile); err != nil {
		return "", fmt.Errorf("failed to read %q: %w", crmetadata.SpecDumpFile, err)
	}

	// Load config.dump from temporary directory
	config := new(crmetadata.ContainerConfig)
	if _, err := crmetadata.ReadJSONFile(config, mountPoint, crmetadata.ConfigDumpFile); err != nil {
		return "", fmt.Errorf("failed to read %q: %w", crmetadata.ConfigDumpFile, err)
	}

	// Load status.dump from temporary directory
	containerStatus := new(runtime.ContainerStatus)
	if _, err := crmetadata.ReadJSONFile(containerStatus, mountPoint, crmetadata.StatusDumpFile); err != nil {
		return "", fmt.Errorf("failed to read %q: %w", crmetadata.StatusDumpFile, err)
	}

	if meta.SandboxID == "" {
		// restore into previous sandbox
		meta.SandboxID = dumpSpec.Annotations[annotations.SandboxID]
		ctrID = config.ID
	} else {
		ctrID = ""
	}

	ctrMetadata := runtime.ContainerMetadata{}

	if meta.Config.Metadata != nil && meta.Config.Metadata.Name != "" {
		ctrMetadata.Name = containerStatus.GetMetadata().GetName()
	}

	originalAnnotations := containerStatus.GetAnnotations()
	originalLabels := containerStatus.GetLabels()

	sandboxUID := sandboxConfig.GetMetadata().GetUid()

	if sandboxUID != "" {
		if _, ok := originalLabels[kubetypes.KubernetesPodUIDLabel]; ok {
			originalLabels[kubetypes.KubernetesPodUIDLabel] = sandboxUID
		}
		if _, ok := originalAnnotations[kubetypes.KubernetesPodUIDLabel]; ok {
			originalAnnotations[kubetypes.KubernetesPodUIDLabel] = sandboxUID
		}
	}

	if createLabels != nil {
		fixupLabels := []string{
			// Update the container name. It has already been update in metadata.Name.
			// It also needs to be updated in the container labels.
			kubetypes.KubernetesContainerNameLabel,
			// Update pod name in the labels.
			kubetypes.KubernetesPodNameLabel,
			// Also update namespace.
			kubetypes.KubernetesPodNamespaceLabel,
		}

		for _, annotation := range fixupLabels {
			_, ok1 := createLabels[annotation]
			_, ok2 := originalLabels[annotation]

			// If the value is not set in the original container or
			// if it is not set in the new container, just skip
			// the step of updating metadata.
			if ok1 && ok2 {
				originalLabels[annotation] = createLabels[annotation]
			}
		}
	}

	if createAnnotations != nil {
		// The hash also needs to be update or Kubernetes thinks the container needs to be restarted
		_, ok1 := createAnnotations["io.kubernetes.container.hash"]
		_, ok2 := originalAnnotations["io.kubernetes.container.hash"]

		if ok1 && ok2 {
			originalAnnotations["io.kubernetes.container.hash"] = createAnnotations["io.kubernetes.container.hash"]
		}

		// The restart count also needs to be correctly updated
		_, ok1 = createAnnotations["io.kubernetes.container.restartCount"]
		_, ok2 = originalAnnotations["io.kubernetes.container.restartCount"]

		if ok1 && ok2 {
			originalAnnotations["io.kubernetes.container.restartCount"] = createAnnotations["io.kubernetes.container.restartCount"]
		}
	}

	// Pulling the image the checkpoint is based on. This is a bit different
	// than automatic image pulling. The checkpoint image is not automatically
	// pulled, but the image the checkpoint is based on.
	// During checkpointing the base image of the checkpoint is stored in the
	// checkpoint archive as NAME@DIGEST. The checkpoint archive also contains
	// the tag with which it was initially pulled.
	// First step is to pull NAME@DIGEST
	containerdImage, err := c.client.Pull(ctx, config.RootfsImageRef)
	if err != nil {
		return "", fmt.Errorf("failed to pull checkpoint base image %s: %w", config.RootfsImageRef, err)
	}
	if _, err := reference.ParseAnyReference(config.RootfsImageName); err != nil {
		return "", fmt.Errorf("error parsing reference: %q is not a valid repository/tag %v", config.RootfsImageName, err)
	}
	tagImage, err := c.client.ImageService().Get(ctx, config.RootfsImageRef)
	if err != nil {
		return "", fmt.Errorf("failed to get checkpoint base image %s: %w", config.RootfsImageRef, err)
	}
	// Second step is to tag the image with the same tag it used to have
	// during checkpointing. For the error that the image NAME:TAG already
	// exists is ignored. It could happen that NAME:TAG now belongs to
	// another NAME@DIGEST than during checkpointing and the restore will
	// happen on another image.
	// TODO: handle if NAME:TAG points to a different NAME@DIGEST
	tagImage.Name = config.RootfsImageName
	_, err = c.client.ImageService().Create(ctx, tagImage)
	if err != nil {
		if !errdefs.IsAlreadyExists(err) {
			return "", fmt.Errorf("failed to tag checkpoint base image %s with %s: %w", config.RootfsImageRef, config.RootfsImageName, err)
		}
	}

	var image imagestore.Image
	for i := 1; i < 500; i++ {
		// This is probably wrong. Not sure how to wait for an image to appear in
		// the image (or content) store.
		log.G(ctx).Debugf("Trying to resolve %s:%d", containerdImage.Name(), i)
		image, err = c.LocalResolve(containerdImage.Name())
		if err == nil {
			break
		}
		time.Sleep(time.Microsecond * time.Duration(i))
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve image %q during checkpoint import: %w", config.RootfsImageName, err)
	}
	imageConfig := image.ImageSpec.Config
	env := append([]string{}, imageConfig.Env...)
	for _, e := range meta.Config.GetEnvs() {
		env = append(env, e.GetKey()+"="+e.GetValue())
	}
	imageConfig.Env = append(imageConfig.Env, env...)

	originalAnnotations["restored"] = "true"
	originalAnnotations["checkpointedAt"] = config.CheckpointedAt.Format(time.RFC3339Nano)
	originalAnnotations["checkpointImage"] = meta.Config.Image.GetUserSpecifiedImage()

	meta.Config.Annotations = originalAnnotations

	// Remove the checkpoint image name and show the base image name in the metadata.
	// The checkpoint image name is still available in the annotations.
	meta.Config.Image.Image = containerStatus.Image.GetImage()
	meta.Config.Image.UserSpecifiedImage = containerStatus.Image.GetUserSpecifiedImage()

	cstatus, err := c.sandboxService.SandboxStatus(ctx, sandbox.Sandboxer, sandbox.ID, false)
	if err != nil {
		return "", fmt.Errorf("failed to get controller status: %w", err)
	}

	containerRootDir, err := c.createContainer(
		&createContainerRequest{
			ctx:                   ctx,
			containerID:           meta.ID,
			sandbox:               sandbox,
			sandboxID:             meta.SandboxID,
			imageID:               image.ID,
			containerConfig:       meta.Config,
			imageConfig:           &imageConfig,
			podSandboxConfig:      sandboxConfig,
			sandboxRuntimeHandler: sandbox.Metadata.RuntimeHandler,
			sandboxPid:            cstatus.Pid,
			NetNSPath:             sandbox.NetNSPath,
			containerName:         containerName,
			containerdImage:       &containerdImage,
			meta:                  meta,
			restore:               true,
			start:                 start,
		},
	)
	if err != nil {
		return "", err
	}

	if restoreStorageImageID != "" {
		if err := fs.CopyDir(containerRootDir, mountPoint); err != nil {
			return "", err
		}
		if err := mount.UnmountAll(mountPoint, 0); err != nil {
			return "", err
		}
	} else {
		// unpack the checkpoint archive
		filter := archive.WithFilter(func(hdr *tar.Header) (bool, error) {
			excludePatterns := []string{
				crmetadata.ConfigDumpFile,
				crmetadata.SpecDumpFile,
				crmetadata.StatusDumpFile,
			}

			for _, pattern := range excludePatterns {
				if strings.HasPrefix(hdr.Name, pattern) {
					return false, nil
				}
			}

			return true, nil
		})

		// Start from the beginning of the checkpoint archive
		archiveFile.Seek(0, 0)
		_, err = archive.Apply(ctx, containerRootDir, archiveFile, []archive.ApplyOpt{filter}...)

		if err != nil {
			return "", fmt.Errorf("unpacking of checkpoint archive %s failed: %w", containerRootDir, err)
		}
	}
	log.G(ctx).Debugf("Unpacked checkpoint in %s", containerRootDir)

	// Restore container log file (if it exists)
	containerLog := filepath.Join(containerRootDir, "container.log")
	_, err = c.os.Stat(containerLog)
	if err == nil {
		if err := c.os.CopyFile(containerLog, meta.LogPath, 0600); err != nil {
			return "", fmt.Errorf("restoring container log file %s failed: %w", containerLog, err)
		}
	}
	return meta.ID, nil
}

func (c *criService) CheckpointContainer(ctx context.Context, r *runtime.CheckpointContainerRequest) (*runtime.CheckpointContainerResponse, error) {
	start := time.Now()
	if err := utils.CheckForCriu(utils.PodCriuVersion); err != nil {
		errorMessage := fmt.Sprintf(
			"CRIU binary not found or too old (<%d). Failed to checkpoint container %q",
			utils.PodCriuVersion,
			r.GetContainerId(),
		)
		log.G(ctx).WithError(err).Error(errorMessage)
		return nil, fmt.Errorf(
			"%s: %w",
			errorMessage,
			err,
		)
	}

	criContainerStatus, err := c.ContainerStatus(ctx, &runtime.ContainerStatusRequest{
		ContainerId: r.GetContainerId(),
	})
	if err != nil {
		return nil, fmt.Errorf("an error occurred when trying to find container the container status %q: %w", r.GetContainerId(), err)
	}

	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when trying to find container %q: %w", r.GetContainerId(), err)
	}

	state := container.Status.Get().State()
	if state != runtime.ContainerState_CONTAINER_RUNNING {
		return nil, fmt.Errorf(
			"container %q is in %s state. only %s containers can be checkpointed",
			r.GetContainerId(),
			criContainerStateToString(state),
			criContainerStateToString(runtime.ContainerState_CONTAINER_RUNNING),
		)
	}

	i, err := container.Container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("get container info: %w", err)
	}

	configJSON, err := json.Marshal(&crmetadata.ContainerConfig{
		ID:              container.ID,
		Name:            container.Name,
		RootfsImageName: criContainerStatus.GetStatus().GetImage().GetImage(),
		RootfsImageRef:  criContainerStatus.GetStatus().GetImageRef(),
		OCIRuntime:      i.Runtime.Name,
		RootfsImage:     criContainerStatus.GetStatus().GetImage().GetImage(),
		CheckpointedAt:  time.Now(),
		CreatedTime:     i.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("generating container config JSON failed: %w", err)
	}

	task, err := container.Container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task for container %q: %w", r.GetContainerId(), err)
	}
	img, err := task.Checkpoint(ctx, []client.CheckpointTaskOpts{withCheckpointOpts(i.Runtime.Name, c.getContainerRootDir(r.GetContainerId()))}...)
	if err != nil {
		return nil, fmt.Errorf("checkpointing container %q failed: %w", r.GetContainerId(), err)
	}

	// the checkpoint image has been provided as an index with manifests representing the tar of criu data, the rw layer, and the config
	var (
		index        v1.Index
		rawIndex     []byte
		targetDesc   = img.Target()
		contentStore = img.ContentStore()
	)

	// Once all content from the checkpoint image has been saved, the
	// checkpoint image can be remove from the local image store.
	defer c.client.ImageService().Delete(ctx, img.Metadata().Name)

	rawIndex, err = content.ReadBlob(ctx, contentStore, targetDesc)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve checkpoint index blob from content store: %w", err)
	}
	if err = json.Unmarshal(rawIndex, &index); err != nil {
		return nil, fmt.Errorf("failed to unmarshall blob into checkpoint data OCI index: %w", err)
	}

	cpPath := filepath.Join(c.getContainerRootDir(r.GetContainerId()), "ctrd-checkpoint")
	if err := os.MkdirAll(cpPath, 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(cpPath)

	// This internal containerd file is used by checkpointctl for
	// checkpoint archive analysis.
	if err := c.os.CopyFile(
		filepath.Join(c.getContainerRootDir(r.GetContainerId()), crmetadata.StatusFile),
		filepath.Join(cpPath, crmetadata.StatusFile),
		0o600,
	); err != nil {
		return nil, err
	}

	// This file is created by CRIU and includes timing analysis.
	// Also used by checkpointctl
	if err := c.os.CopyFile(
		filepath.Join(c.getContainerRootDir(r.GetContainerId()), stats.StatsDump),
		filepath.Join(cpPath, stats.StatsDump),
		0o600,
	); err != nil {
		return nil, err
	}

	// The log file created by CRIU. This file could be missing.
	// Let's ignore errors if the file is missing.
	if err := c.os.CopyFile(
		filepath.Join(c.getContainerRootDir(r.GetContainerId()), crmetadata.DumpLogFile),
		filepath.Join(cpPath, crmetadata.DumpLogFile),
		0o600,
	); err != nil {
		if !errors.Is(errors.Unwrap(err), os.ErrNotExist) {
			return nil, err
		}
	}

	// Save the existing container log file
	_, err = c.os.Stat(criContainerStatus.GetStatus().GetLogPath())
	if err == nil {
		if err := c.os.CopyFile(
			criContainerStatus.GetStatus().GetLogPath(),
			filepath.Join(cpPath, "container.log"),
			0o600,
		); err != nil {
			return nil, err
		}
	}

	if err := os.WriteFile(filepath.Join(cpPath, crmetadata.ConfigDumpFile), configJSON, 0o600); err != nil {
		return nil, err
	}

	containerStatus, err := json.Marshal(criContainerStatus.GetStatus())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal container status: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cpPath, crmetadata.StatusDumpFile), containerStatus, 0o600); err != nil {
		return nil, err
	}

	// walk the manifests and pull out the blobs that we need to save in the checkpoint tarball:
	// - the checkpoint criu data
	// - the rw diff tarball
	// - the spec blob
	for _, manifest := range index.Manifests {
		switch manifest.MediaType {
		case images.MediaTypeContainerd1Checkpoint:
			if err := writeCriuCheckpointData(ctx, contentStore, manifest, cpPath); err != nil {
				return nil, fmt.Errorf("failed to copy CRIU checkpoint blob to checkpoint dir: %w", err)
			}
		case v1.MediaTypeImageLayerGzip:
			if err := writeRootFsDiffTar(ctx, contentStore, manifest, cpPath); err != nil {
				return nil, fmt.Errorf("failed to copy rw filesystem layer blob to checkpoint dir: %w", err)
			}
		case images.MediaTypeContainerd1CheckpointConfig:
			if err := writeSpecDumpFile(ctx, contentStore, manifest, cpPath); err != nil {
				return nil, fmt.Errorf("failed to copy container spec blob to checkpoint dir: %w", err)
			}
		default:
		}
	}

	// write final tarball of all content
	tar := archive.Diff(ctx, "", cpPath)

	outFile, err := os.OpenFile(r.Location, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	defer outFile.Close()
	_, err = io.Copy(outFile, tar)
	if err != nil {
		return nil, err
	}
	if err := tar.Close(); err != nil {
		return nil, err
	}

	containerCheckpointTimer.WithValues(i.Runtime.Name).UpdateSince(start)

	log.G(ctx).Infof("Wrote checkpoint archive to %s for %s", outFile.Name(), r.GetContainerId())

	return &runtime.CheckpointContainerResponse{}, nil
}

func withCheckpointOpts(rt, rootDir string) client.CheckpointTaskOpts {
	return func(r *client.CheckpointTaskInfo) error {
		// Kubernetes currently supports checkpointing of container
		// as part of the Forensic Container Checkpointing KEP.
		// This implies that the container is never stopped
		leaveRunning := true

		switch rt {
		case plugins.RuntimeRuncV2:
			if r.Options == nil {
				r.Options = &options.CheckpointOptions{}
			}
			opts, _ := r.Options.(*options.CheckpointOptions)

			opts.Exit = !leaveRunning
			opts.WorkPath = rootDir
		}
		return nil
	}
}

func writeCriuCheckpointData(ctx context.Context, store content.Store, desc v1.Descriptor, cpPath string) error {
	ra, err := store.ReaderAt(ctx, desc)
	if err != nil {
		return err
	}
	defer ra.Close()

	checkpointDirectory := filepath.Join(cpPath, crmetadata.CheckpointDirectory)
	// This is the criu data tarball. Let's unpack it
	// and put it into the crmetadata.CheckpointDirectory directory.
	if err := os.MkdirAll(checkpointDirectory, 0o700); err != nil {
		return err
	}
	tr := tar.NewReader(content.NewReader(ra))
	for {
		header, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if strings.Contains(header.Name, "..") {
			return fmt.Errorf("found illegal string '..' in checkpoint archive")
		}
		destFile, err := os.Create(filepath.Join(checkpointDirectory, header.Name))
		if err != nil {
			return err
		}
		defer destFile.Close()

		_, err = io.CopyN(destFile, tr, header.Size)
		if err != nil {
			return err
		}
	}
	return nil
}

func writeRootFsDiffTar(ctx context.Context, store content.Store, desc v1.Descriptor, cpPath string) error {
	ra, err := store.ReaderAt(ctx, desc)
	if err != nil {
		return err
	}
	defer ra.Close()

	// the rw layer tarball
	f, err := os.Create(filepath.Join(cpPath, crmetadata.RootFsDiffTar))
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, content.NewReader(ra))
	if err != nil {
		return err
	}

	return nil
}

func writeSpecDumpFile(ctx context.Context, store content.Store, desc v1.Descriptor, cpPath string) error {
	// this is the container spec
	f, err := os.Create(filepath.Join(cpPath, crmetadata.SpecDumpFile))
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := content.ReadBlob(ctx, store, desc)
	if err != nil {
		return err
	}
	var any ptypes.Any
	if err := proto.Unmarshal(data, &any); err != nil {
		return err
	}
	_, err = f.Write(any.Value)
	if err != nil {
		return err
	}

	return nil
}
