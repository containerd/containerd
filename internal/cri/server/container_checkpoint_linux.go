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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	crmetadata "github.com/checkpoint-restore/checkpointctl/lib"
	criu "github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/utils"
	"github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/log"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	// TODO: This package import is kept to prevent merge conflicts while integrating multiple
	// branches, specifically because this changes vendoring.
	_ "github.com/checkpoint-restore/go-criu/v7/stats"
)

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

func (c *criService) checkCriu() error {
	c.checkCriuOnce.Do(func() {
		c.checkCriuErr = c.doCheckCriu()
	})
	return c.checkCriuErr
}

func (c *criService) doCheckCriu() error {
	if c.config.EnableCRIU != nil && !*c.config.EnableCRIU {
		return errors.New("criu support is disabled by configuration")
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

func (c *criService) CheckpointContainer(ctx context.Context, r *runtime.CheckpointContainerRequest) (*runtime.CheckpointContainerResponse, error) {
	start := time.Now()
	if err := c.checkCriu(); err != nil {
		log.G(ctx).WithError(err).Errorf("Failed to checkpoint container %q", r.GetContainerId())
		return nil, fmt.Errorf("failed to checkpoint container %q: %w", r.GetContainerId(), err)
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
			container.ID,
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
		return nil, fmt.Errorf("failed to get task for container %q: %w", container.ID, err)
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

	// This internal containerd file is used by checkpointctl for checkpoint archive
	// analysis. It lives in the container state dir, which can hold files from a
	// prior checkpoint operation, so it is read without following symlinks.
	if err := copyNoFollow(
		filepath.Join(c.getContainerRootDir(container.ID), crmetadata.StatusFile),
		filepath.Join(cpPath, crmetadata.StatusFile),
		0o600,
	); err != nil {
		return nil, err
	}

	// dump.log and stats-dump are written directly into cpPath by CRIU via its
	// work directory (see withCheckpointOpts above), so they are already present
	// for archiving and do not need to be copied out of the container state dir.

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

	log.G(ctx).Infof("Wrote checkpoint archive to %s for %s", outFile.Name(), container.ID)

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
