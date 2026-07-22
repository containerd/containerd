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
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	crmetadata "github.com/checkpoint-restore/checkpointctl/lib"
	criu "github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/stats"
	"github.com/checkpoint-restore/go-criu/v7/utils"
	"github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/cri/annotations"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	customopts "github.com/containerd/containerd/v2/internal/cri/opts"
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
	"golang.org/x/sys/unix"
	gproto "google.golang.org/protobuf/proto"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// spec.dump records the resolved process, but not which values came from the
// CRI request. Preserve that request so RestorePod can detect removed defaults
// and settings that a runtime may otherwise silently ignore.
const criContainerConfigDumpFile = "cri-container-config.json"

// copyNoFollow copies the regular file at src to dst without following a symlink
// at the final path component of src.
//
// The checkpoint code reads files (container.log, status, stats-dump, dump.log)
// out of the container state directory, which can contain entries unpacked from a
// checkpoint archive or OCI image. Those entries are externally provided, so they
// are read defensively.
//
// src is first lstat'd (which does not follow a final-component symlink) and must
// be a regular file; non-regular entries are rejected before src is ever opened.
// src is then opened with O_NOFOLLOW as a belt-and-suspenders guard in case the
// entry changes type between the lstat and the open.
func copyNoFollow(src, dst string, perm os.FileMode) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to copy %s: not a regular file", src)
	}

	in, err := os.OpenFile(src, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// copyCheckpointMetadataIfMissing preserves files written directly into the
// current checkpoint work directory. Falling back to the container state
// directory is only for runtimes that still publish optional metadata there;
// that directory may contain stale files from an earlier checkpoint.
func copyCheckpointMetadataIfMissing(src, dst string) error {
	info, err := os.Lstat(dst)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint metadata %q is not a regular file", dst)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := copyNoFollow(src, dst, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// checkpointArchiveEntryAllowed reports whether a tar entry from a checkpoint
// archive may be unpacked. Legitimate checkpoint archives contain only regular
// files and directories; other entry types (symlinks, hardlinks, device and fifo
// nodes) are not produced by the checkpoint code and are rejected as a hardening
// measure.
func checkpointArchiveEntryAllowed(hdr *tar.Header) bool {
	switch hdr.Typeflag {
	//nolint:staticcheck // TypeRegA is deprecated but we may still receive an external tar with TypeRegA
	case tar.TypeReg, tar.TypeRegA, tar.TypeDir, tar.TypeXGlobalHeader:
		return true
	default:
		return false
	}
}

// assertCheckpointDirSafe verifies that the populated restore directory contains
// only regular files and directories.
//
// The OCI-image restore path copies checkpoint content into the restore dir with
// fs.CopyDir, which (unlike the tar unpack filter) faithfully recreates any
// symlinks and special files present in the image. Restore-time consumers open
// paths under this directory, so non-regular entries are rejected before they run.
func assertCheckpointDirSafe(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// d.Type() reports the entry type without following symlinks.
		if d.IsDir() || d.Type().IsRegular() {
			return nil
		}
		return fmt.Errorf("refusing to restore checkpoint: %s is not a regular file or directory", path)
	})
}

func (c *criService) checkCriu() error {
	c.checkCriuOnce.Do(func() {
		c.checkCriuErr = c.doCheckCriu()
	})
	return c.checkCriuErr
}

// checkCriuConfig only checks the configuration gate; unlike checkCriu it does
// not require a CRIU binary, so it applies even to runtimes that implement
// checkpoint natively.
func (c *criService) checkCriuConfig() error {
	if c.config.EnableCRIU != nil && !*c.config.EnableCRIU {
		return errors.New("criu support is disabled by configuration")
	}
	return nil
}

func (c *criService) doCheckCriu() error {
	if err := c.checkCriuConfig(); err != nil {
		return err
	}
	path := resolveCriuPath(c.shimPath)
	if path == "" {
		return errors.New("criu binary not found in shim path or system PATH")
	}
	client := criu.MakeCriu()
	client.SetCriuPath(path)
	version, err := client.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to retrieve criu version: %w", err)
	}
	if version < utils.PodCriuVersion {
		return fmt.Errorf("checkpoint/restore requires at least CRIU %d, current version is %d", utils.PodCriuVersion, version)
	}
	return nil
}

func resolveCriuPath(customPath string) string {
	if customPath != "" {
		// This logic is Linux-specific. If CRIU is ever supported on other
		// operating systems, path lookup will need to respect that OS's
		// conventions.
		for _, dir := range filepath.SplitList(customPath) {
			if !filepath.IsAbs(dir) {
				continue
			}
			criuPath := filepath.Join(dir, "criu")
			if fi, err := os.Stat(criuPath); err == nil && fi.Mode().IsRegular() && fi.Mode()&0111 != 0 {
				return criuPath
			}
		}
		return ""
	}
	if criuPath, err := exec.LookPath("criu"); err == nil {
		if absPath, err := filepath.Abs(criuPath); err == nil {
			return absPath
		}
		return criuPath
	}
	return ""
}

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
	if err := c.checkCriu(); err != nil {
		return "", fmt.Errorf("checkpoint restore is not enabled: %w", err)
	}

	var mountPoint string
	start := time.Now()
	// Ensure that the image to restore the checkpoint from has been provided.
	if meta.Config.Image == nil || meta.Config.Image.Image == "" {
		return "", errors.New(`attribute "image" missing from container definition`)
	}

	inputImage := meta.Config.Image.Image
	restoreUserSpecifiedImage := meta.Config.Image.GetUserSpecifiedImage()
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
			// Reject entry types the checkpoint code never produces (symlinks,
			// hardlinks, device/fifo nodes) so they are not recreated on disk.
			if !checkpointArchiveEntryAllowed(hdr) {
				log.G(ctx).Warnf("Skipping unexpected checkpoint archive entry %q (type %d)", hdr.Name, hdr.Typeflag)
				return false, nil
			}
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
	checkpointContainerConfig := new(runtime.ContainerConfig)
	if _, err := crmetadata.ReadJSONFile(checkpointContainerConfig, mountPoint, criContainerConfigDumpFile); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("failed to read %q: %w", criContainerConfigDumpFile, err)
		}
		checkpointContainerConfig = nil
	}
	restoreRuntime, err := c.config.GetSandboxRuntime(sandboxConfig, sandbox.Metadata.RuntimeHandler)
	if err != nil {
		return "", fmt.Errorf("failed to resolve restore runtime handler %q: %w", sandbox.Metadata.RuntimeHandler, err)
	}
	if err := validateCheckpointRuntime(config.OCIRuntime, restoreRuntime.Type); err != nil {
		return "", err
	}

	if meta.SandboxID == "" {
		// restore into previous sandbox
		meta.SandboxID = dumpSpec.Annotations[annotations.SandboxID]
		ctrID = config.ID
	} else {
		ctrID = ""
	}

	var containerdImage client.Image

	containerdImage, err = c.client.GetImage(ctx, config.RootfsImageRef)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return "", fmt.Errorf("failed to get checkpoint base image %s: %w", config.RootfsImageRef, err)
		}
		// Pulling the image the checkpoint is based on. This is a bit different
		// than automatic image pulling. The checkpoint image is not automatically
		// pulled, but the image the checkpoint is based on.
		// During checkpointing the base image of the checkpoint is stored in the
		// checkpoint archive as NAME@DIGEST. The checkpoint archive also contains
		// the tag with which it was initially pulled.
		// First step is to pull NAME@DIGEST
		containerdImage, err = c.client.Pull(ctx, config.RootfsImageRef)
		if err != nil {
			return "", fmt.Errorf("failed to pull checkpoint base image %s: %w", config.RootfsImageRef, err)
		}
	}

	if _, err := reference.ParseAnyReference(config.RootfsImageName); err != nil {
		return "", fmt.Errorf("error parsing reference: %q is not a valid repository/tag %v", config.RootfsImageName, err)
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
	if isPodRestoreContainerContext(ctx) {
		processConfig := meta.Config
		if checkpointContainerConfig != nil {
			if err := validateRestoreContainerConfig(checkpointContainerConfig, meta.Config); err != nil {
				return "", err
			}
			processConfig = checkpointContainerConfig
		} else if err := validateCheckpointImage(meta.Config.GetImage().GetUserSpecifiedImage(), config.RootfsImageName); err != nil {
			return "", err
		}
		if err := validateCheckpointProcess(dumpSpec, processConfig, &imageConfig); err != nil {
			return "", err
		}
	}

	originalLabels, originalAnnotations := restoreContainerMetadata(
		ctx,
		containerStatus.GetLabels(),
		containerStatus.GetAnnotations(),
		createLabels,
		createAnnotations,
		sandboxConfig.GetMetadata().GetUid(),
	)
	if !isPodRestoreContainerContext(ctx) {
		if originalAnnotations == nil {
			originalAnnotations = make(map[string]string)
		}
		originalAnnotations["restored"] = "true"
		originalAnnotations["checkpointedAt"] = config.CheckpointedAt.Format(time.RFC3339Nano)
		originalAnnotations["checkpointImage"] = inputImage
	}

	meta.Config.Labels = originalLabels
	meta.Config.Annotations = originalAnnotations

	// The archive path was only a transport for CreateContainer. Report the base
	// image while retaining the Pod restore request's user-specified image name.
	meta.Config.Image.Image = containerStatus.Image.GetImage()
	if isPodRestoreContainerContext(ctx) {
		meta.Config.Image.UserSpecifiedImage = restoreUserSpecifiedImage
	} else {
		meta.Config.Image.UserSpecifiedImage = containerStatus.Image.GetUserSpecifiedImage()
	}

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
			containerName:         meta.Config.GetMetadata().GetName(),
			containerdImage:       &containerdImage,
			meta:                  meta,
			restore:               true,
			start:                 start,
		},
	)
	if err != nil {
		return "", err
	}

	// Confine all checkpoint content to a dedicated subdirectory of the container
	// state dir instead of unpacking it directly into the state dir, so it cannot
	// collide with containerd's own files there. Create it fresh; RemoveAll unlinks
	// any pre-existing entry without following it.
	restoreDir := filepath.Join(containerRootDir, checkpointRestoreDir)
	if err := os.RemoveAll(restoreDir); err != nil {
		return "", err
	}
	if err := os.Mkdir(restoreDir, 0o700); err != nil {
		return "", err
	}

	if restoreStorageImageID != "" {
		if err := fs.CopyDir(restoreDir, mountPoint); err != nil {
			return "", err
		}
		if err := mount.UnmountAll(mountPoint, 0); err != nil {
			return "", err
		}
		// fs.CopyDir recreates any symlinks/special files from the image; reject
		// them here so restore-time consumers only ever open regular files.
		if err := assertCheckpointDirSafe(restoreDir); err != nil {
			return "", err
		}
	} else {
		// unpack the checkpoint archive
		filter := archive.WithFilter(func(hdr *tar.Header) (bool, error) {
			// Reject entry types the checkpoint code never produces (symlinks,
			// hardlinks, device/fifo nodes) so they are not recreated on disk.
			if !checkpointArchiveEntryAllowed(hdr) {
				log.G(ctx).Warnf("Skipping unexpected checkpoint archive entry %q (type %d)", hdr.Name, hdr.Typeflag)
				return false, nil
			}
			excludePatterns := []string{
				crmetadata.ConfigDumpFile,
				crmetadata.SpecDumpFile,
				crmetadata.StatusDumpFile,
				criContainerConfigDumpFile,
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
		_, err = archive.Apply(ctx, restoreDir, archiveFile, []archive.ApplyOpt{filter}...)

		if err != nil {
			return "", fmt.Errorf("unpacking of checkpoint archive %s failed: %w", restoreDir, err)
		}
	}
	log.G(ctx).Debugf("Unpacked checkpoint in %s", restoreDir)
	// Keep rootfs-diff.tar beside the checkpoint directory. StartContainer passes
	// that directory through WithTaskCheckpointPath, and runtime-v2 applies the
	// sibling diff after mounting the fresh snapshot but before starting CRIU.

	// Restore container log file (if it exists).
	//
	// container.log was unpacked from a checkpoint archive/OCI image, so it is
	// copied without following a final-component symlink.
	containerLog := filepath.Join(restoreDir, "container.log")
	if logDir := filepath.Dir(meta.LogPath); logDir != "" {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return "", fmt.Errorf("creating log directory %s failed: %w", logDir, err)
		}
	}
	if err := copyNoFollow(containerLog, meta.LogPath, 0600); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("restoring container log file %s failed: %w", containerLog, err)
		}
	}
	return meta.ID, nil
}

func (c *criService) CheckpointContainer(ctx context.Context, r *runtime.CheckpointContainerRequest) (*runtime.CheckpointContainerResponse, error) {
	start := time.Now()
	if err := c.checkCriuConfig(); err != nil {
		log.G(ctx).WithError(err).Errorf("Failed to checkpoint container %q", r.GetContainerId())
		return nil, fmt.Errorf("failed to checkpoint container %q: %w", r.GetContainerId(), err)
	}

	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when trying to find container %q: %w", r.GetContainerId(), err)
	}
	release, err := c.reserveContainerCheckpoints([]string{container.ID})
	if err != nil {
		return nil, err
	}
	defer release()

	return c.checkpointContainerLocked(ctx, r, container, nil, start)
}

// reserveContainerCheckpoints reserves every canonical container ID before the
// caller proceeds. Reservations acquired before a conflict are released before
// the error is returned.
func (c *criService) reserveContainerCheckpoints(containerIDs []string) (func(), error) {
	reserved := make([]string, 0, len(containerIDs))
	release := func() {
		for i := len(reserved) - 1; i >= 0; i-- {
			c.containerCheckpointsInProgress.Delete(reserved[i])
		}
	}
	for _, containerID := range containerIDs {
		if _, loaded := c.containerCheckpointsInProgress.LoadOrStore(containerID, struct{}{}); loaded {
			release()
			return nil, fmt.Errorf("checkpoint for container %q is already in progress", containerID)
		}
		reserved = append(reserved, containerID)
	}
	return release, nil
}

// checkpointContainerLocked checkpoints a resolved container. The caller must
// hold its containerCheckpointsInProgress reservation for the entire call.
func (c *criService) checkpointContainerLocked(ctx context.Context, r *runtime.CheckpointContainerRequest, container containerstore.Container, task client.Task, start time.Time) (*runtime.CheckpointContainerResponse, error) {
	criContainerStatus, err := c.ContainerStatus(ctx, &runtime.ContainerStatusRequest{
		ContainerId: container.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("an error occurred when trying to find container the container status %q: %w", container.ID, err)
	}

	state := container.Status.Get().State()
	if state != runtime.ContainerState_CONTAINER_RUNNING {
		return nil, fmt.Errorf(
			"container %q is in %s state. only %s containers can be checkpointed",
			container.ID,
			criContainerStateToString(state),
			criContainerStateToString(runtime.ContainerState_CONTAINER_RUNNING),
		)
	}

	i, err := container.Container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("get container info: %w", err)
	}

	// CRIU is only required for runc-based runtimes. Other runtimes
	// (e.g. runsc/gVisor) implement checkpoint natively.
	if i.Runtime.Name == plugins.RuntimeRuncV2 {
		if err := c.checkCriu(); err != nil {
			log.G(ctx).WithError(err).Errorf("Failed to checkpoint container %q", r.GetContainerId())
			return nil, fmt.Errorf("failed to checkpoint container %q: %w", r.GetContainerId(), err)
		}
	}

	rootfsImageName := container.Config.GetImage().GetUserSpecifiedImage()
	if rootfsImageName == "" {
		rootfsImageName = criContainerStatus.GetStatus().GetImage().GetImage()
	}
	configJSON, err := json.Marshal(&crmetadata.ContainerConfig{
		ID:              container.ID,
		Name:            container.Name,
		RootfsImageName: rootfsImageName,
		RootfsImageRef:  criContainerStatus.GetStatus().GetImageRef(),
		OCIRuntime:      i.Runtime.Name,
		RootfsImage:     criContainerStatus.GetStatus().GetImage().GetImage(),
		CheckpointedAt:  time.Now(),
		CreatedTime:     i.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("generating container config JSON failed: %w", err)
	}

	if task == nil {
		task, err = container.Container.Task(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get task for container %q: %w", container.ID, err)
		}
	}

	cpPath := filepath.Join(c.getContainerRootDir(container.ID), "ctrd-checkpoint")
	// ctrd-checkpoint may already exist from a prior checkpoint operation. RemoveAll
	// unlinks any existing entry (including a symlink) itself rather than its target,
	// so creating the directory afterwards cannot write through a link.
	if err := os.RemoveAll(cpPath); err != nil {
		return nil, err
	}
	if err := os.Mkdir(cpPath, 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(cpPath)

	// Point CRIU's work directory (where it writes dump.log and stats-dump) at the
	// dedicated, freshly-created checkpoint dir instead of the persistent container
	// state dir. Otherwise checkpoint creation litters those files into the state
	// dir where they are never cleaned up; here they land directly where they are
	// archived from and are removed with cpPath.
	img, err := task.Checkpoint(ctx, []client.CheckpointTaskOpts{withCheckpointOpts(i.Runtime.Name, cpPath)}...)
	if err != nil {
		return nil, fmt.Errorf("checkpointing container %q failed: %w", container.ID, err)
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

	// Some runtimes write optional checkpointctl metadata into the checkpoint
	// work directory while others leave it in the container state directory.
	// Prefer the current work-directory copy so stale state cannot replace fresh
	// CRIU diagnostics; missing optional files are tolerated.
	optionalFiles := []string{
		crmetadata.StatusFile,
		stats.StatsDump,
		crmetadata.DumpLogFile,
	}
	for _, name := range optionalFiles {
		src := filepath.Join(c.getContainerRootDir(container.ID), name)
		if err := copyCheckpointMetadataIfMissing(src, filepath.Join(cpPath, name)); err != nil {
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
	checkpointContainerConfig, err := json.Marshal(container.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal checkpoint container config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cpPath, criContainerConfigDumpFile), checkpointContainerConfig, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write checkpoint container config: %w", err)
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

	if err := writeCheckpointArchiveAtomic(ctx, r.Location, cpPath); err != nil {
		return nil, err
	}

	containerCheckpointTimer.WithValues(i.Runtime.Name).UpdateSince(start)

	log.G(ctx).Infof("Wrote checkpoint archive to %s for %s", r.Location, container.ID)

	return &runtime.CheckpointContainerResponse{}, nil
}

func writeCheckpointArchiveAtomic(ctx context.Context, location, checkpointDir string) (retErr error) {
	archiveReader := archive.Diff(ctx, "", checkpointDir)
	archiveClosed := false
	defer func() {
		if !archiveClosed {
			retErr = errors.Join(retErr, archiveReader.Close())
		}
	}()

	destinationDir := filepath.Dir(location)
	temp, err := os.CreateTemp(destinationDir, ".checkpoint-tmp-")
	if err != nil {
		return fmt.Errorf("failed to create temporary checkpoint archive beside %q: %w", location, err)
	}
	tempPath := temp.Name()
	tempClosed := false
	defer func() {
		if !tempClosed {
			retErr = errors.Join(retErr, temp.Close())
		}
		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("failed to remove temporary checkpoint archive %q: %w", tempPath, err))
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("failed to set temporary checkpoint archive permissions: %w", err)
	}
	if _, err := io.Copy(temp, archiveReader); err != nil {
		return fmt.Errorf("failed to write temporary checkpoint archive: %w", err)
	}
	if err := archiveReader.Close(); err != nil {
		return fmt.Errorf("failed to finish checkpoint archive: %w", err)
	}
	archiveClosed = true
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("failed to persist temporary checkpoint archive: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("failed to close temporary checkpoint archive: %w", err)
	}
	tempClosed = true
	if err := os.Rename(tempPath, location); err != nil {
		return fmt.Errorf("failed to publish checkpoint archive %q: %w", location, err)
	}
	if err := syncDirectory(destinationDir); err != nil {
		return fmt.Errorf("failed to persist checkpoint archive %q: %w", location, err)
	}
	return nil
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

func filterAndMergeAnnotations(
	ctx context.Context,
	checkpointAnnotations map[string]string,
	createAnnotations map[string]string,
) map[string]string {
	result := make(map[string]string)

	for k, v := range checkpointAnnotations {
		if strings.HasPrefix(k, "cdi.k8s.io/") || k == "cdi.k8s.io" {
			log.G(ctx).Warnf("Denying annotation %q in checkpoint restore", k)
			continue
		}
		result[k] = v
	}
	// Only checkpoint annotations are replayed data. Restore-time annotations
	// come from the current CRI request and remain subject to normal create-time
	// processing.
	maps.Copy(result, createAnnotations)
	return result
}

func restoreContainerMetadata(
	ctx context.Context,
	checkpointLabels, checkpointAnnotations map[string]string,
	createLabels, createAnnotations map[string]string,
	sandboxUID string,
) (map[string]string, map[string]string) {
	if isPodRestoreContainerContext(ctx) {
		// RestorePod carries a complete current ContainerConfig. Replaying stale
		// checkpoint metadata here can change OCI generation through annotations.
		return maps.Clone(createLabels), maps.Clone(createAnnotations)
	}

	checkpointLabels = maps.Clone(checkpointLabels)
	checkpointAnnotations = maps.Clone(checkpointAnnotations)
	if sandboxUID != "" {
		if _, ok := checkpointLabels[crilabels.KubernetesPodUIDLabel]; ok {
			checkpointLabels[crilabels.KubernetesPodUIDLabel] = sandboxUID
		}
		if _, ok := checkpointAnnotations[crilabels.KubernetesPodUIDLabel]; ok {
			checkpointAnnotations[crilabels.KubernetesPodUIDLabel] = sandboxUID
		}
	}
	return mergeStringMaps(checkpointLabels, createLabels), filterAndMergeAnnotations(ctx, checkpointAnnotations, createAnnotations)
}

func mergeStringMaps(base, overrides map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(overrides))
	maps.Copy(result, base)
	maps.Copy(result, overrides)
	return result
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
	if !gproto.Equal(checkpointSecurity, restoreSecurity) {
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
	// Older archives and direct CRI clients may not retain the user-specified
	// image name. Other process fields are still validated in that case.
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
		env[entry.GetKey()] = entry.GetValue()
	}
	return env
}

func validateCheckpointProcess(dump *spec.Spec, config *runtime.ContainerConfig, image *v1.ImageConfig) error {
	if dump == nil || dump.Process == nil {
		return fmt.Errorf("checkpoint OCI spec has no process configuration: %w", errdefs.ErrFailedPrecondition)
	}
	expected := &spec.Spec{Process: new(spec.Process)}
	if err := customopts.WithProcessArgs(config, image)(context.Background(), nil, nil, expected); err != nil {
		return fmt.Errorf("resolve restore process arguments: %w", err)
	}
	if !slices.Equal(dump.Process.Args, expected.Process.Args) {
		return fmt.Errorf("restore process arguments %q do not match checkpoint process arguments %q: %w", expected.Process.Args, dump.Process.Args, errdefs.ErrFailedPrecondition)
	}

	expectedCwd := config.GetWorkingDir()
	if expectedCwd == "" {
		expectedCwd = image.WorkingDir
	}
	if expectedCwd == "" {
		expectedCwd = "/"
	}
	if dump.Process.Cwd != expectedCwd {
		return fmt.Errorf("restore process working directory %q does not match checkpoint process working directory %q: %w", expectedCwd, dump.Process.Cwd, errdefs.ErrFailedPrecondition)
	}

	expectedEnv, err := expectedProcessEnvironment(config, image)
	if err != nil {
		return err
	}
	checkpointEnv, err := ociProcessEnvironment(dump.Process.Env)
	if err != nil {
		return err
	}
	for key, value := range expectedEnv {
		if checkpointEnv[key] != value {
			return fmt.Errorf("restore environment variable %q does not match the checkpointed process: %w", key, errdefs.ErrFailedPrecondition)
		}
	}
	if dump.Process.Terminal != config.GetTty() {
		return fmt.Errorf("restore TTY setting does not match the checkpointed process: %w", errdefs.ErrFailedPrecondition)
	}

	security := config.GetLinux().GetSecurityContext()
	if runAsUser := security.GetRunAsUser(); runAsUser != nil && int64(dump.Process.User.UID) != runAsUser.GetValue() {
		return fmt.Errorf("restore process UID %d does not match checkpoint process UID %d: %w", runAsUser.GetValue(), dump.Process.User.UID, errdefs.ErrFailedPrecondition)
	}
	if runAsGroup := security.GetRunAsGroup(); runAsGroup != nil && int64(dump.Process.User.GID) != runAsGroup.GetValue() {
		return fmt.Errorf("restore process GID %d does not match checkpoint process GID %d: %w", runAsGroup.GetValue(), dump.Process.User.GID, errdefs.ErrFailedPrecondition)
	}
	for _, group := range security.GetSupplementalGroups() {
		if group < 0 || group > int64(^uint32(0)) || !slices.Contains(dump.Process.User.AdditionalGids, uint32(group)) {
			return fmt.Errorf("restore supplemental group %d is absent from the checkpointed process: %w", group, errdefs.ErrFailedPrecondition)
		}
	}
	if dump.Process.NoNewPrivileges != security.GetNoNewPrivs() {
		return fmt.Errorf("restore no-new-privileges setting does not match the checkpointed process: %w", errdefs.ErrFailedPrecondition)
	}
	return nil
}

func expectedProcessEnvironment(config *runtime.ContainerConfig, image *v1.ImageConfig) (map[string]string, error) {
	env, err := ociProcessEnvironment(image.Env)
	if err != nil {
		return nil, fmt.Errorf("invalid image environment: %w", err)
	}
	maps.Copy(env, containerEnvironment(config))
	return env, nil
}

func ociProcessEnvironment(entries []string) (map[string]string, error) {
	env := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("checkpoint OCI spec contains invalid environment entry %q: %w", entry, errdefs.ErrFailedPrecondition)
		}
		env[key] = value
	}
	return env, nil
}

func validateCheckpointRuntime(checkpointRuntime, restoreRuntime string) error {
	// Older checkpoint archives may not record a runtime. Reject a known
	// mismatch, since feeding runtime-specific state to another shim cannot
	// produce a valid restored container.
	if checkpointRuntime == "" || checkpointRuntime == restoreRuntime {
		return nil
	}
	return fmt.Errorf("checkpoint requires runtime %q, but restored sandbox uses runtime %q: %w", checkpointRuntime, restoreRuntime, errdefs.ErrFailedPrecondition)
}
