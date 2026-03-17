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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	crmetadata "github.com/checkpoint-restore/checkpointctl/lib"
	"github.com/checkpoint-restore/go-criu/v7/stats"
	"github.com/checkpoint-restore/go-criu/v7/utils"
	"github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/internal/cri/opts"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/version"
	"github.com/containerd/log"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"golang.org/x/sys/unix"
)

func (c *criService) CheckpointPod(ctx context.Context, r *runtime.CheckpointPodRequest) (*runtime.CheckpointPodResponse, error) {
	start := time.Now()

	if err := utils.CheckForCriu(utils.PodCriuVersion); err != nil {
		return nil, fmt.Errorf(
			"CRIU binary not found or too old (<%d). Failed to checkpoint pod %q: %w",
			utils.PodCriuVersion,
			r.GetPodSandboxId(),
			err,
		)
	}

	if r.GetPath() == "" {
		return nil, fmt.Errorf("path is required for pod checkpoint")
	}

	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when trying to find sandbox %q: %w", r.GetPodSandboxId(), err)
	}

	sandboxConfig := sandbox.Metadata.Config

	log.G(ctx).Infof("Checkpointing pod sandbox %s", r.GetPodSandboxId())

	// List all containers belonging to this sandbox
	containers := c.containerStore.List()
	var podContainers []containerInfo
	for _, ctr := range containers {
		if ctr.SandboxID != sandbox.ID {
			continue
		}
		state := ctr.Status.Get().State()
		if state != runtime.ContainerState_CONTAINER_RUNNING {
			log.G(ctx).Infof("Skipping container %s in state %s", ctr.ID, criContainerStateToString(state))
			continue
		}
		i, err := ctr.Container.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("get container info for %s: %w", ctr.ID, err)
		}
		podContainers = append(podContainers, containerInfo{
			container: ctr,
			info:      i,
		})
	}

	if len(podContainers) == 0 {
		return nil, fmt.Errorf("no running containers to checkpoint in sandbox %s", sandbox.ID)
	}

	// Create a temporary directory as working area for checkpoint data
	mountPoint, err := os.MkdirTemp("", "pod-checkpoint-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory for pod checkpoint: %w", err)
	}
	defer os.RemoveAll(mountPoint)

	leaveRunning := r.GetOptions()["leaveRunning"] == "true"

	// Phase 1: Pause all containers before checkpointing.
	// This ensures all containers are frozen at roughly the same time
	// for a consistent snapshot.
	pausedTasks := make(map[string]client.Task)
	for _, ci := range podContainers {
		log.G(ctx).Infof("Pausing container %s before pod checkpoint", ci.container.ID)
		task, err := ci.container.Container.Task(ctx, nil)
		if err != nil {
			// Best effort resume already paused tasks
			for id, t := range pausedTasks {
				if err := t.Resume(ctx); err != nil {
					log.G(ctx).Errorf("Failed to resume container %s: %v", id, err)
				}
			}
			return nil, fmt.Errorf("failed to get task for container %s: %w", ci.container.ID, err)
		}
		if err := task.Pause(ctx); err != nil {
			for id, t := range pausedTasks {
				if err := t.Resume(ctx); err != nil {
					log.G(ctx).Errorf("Failed to resume container %s: %v", id, err)
				}
			}
			return nil, fmt.Errorf("failed to pause container %s: %w", ci.container.ID, err)
		}
		pausedTasks[ci.container.ID] = task
	}

	// Defer resuming all paused containers
	defer func() {
		if leaveRunning {
			for id, task := range pausedTasks {
				if err := task.Resume(ctx); err != nil {
					log.G(ctx).Errorf("Failed to resume container %s: %v", id, err)
				}
			}
		}
	}()

	// Phase 2: Checkpoint all containers
	containerNamesMap := make(map[string]string, len(podContainers))

	for _, ci := range podContainers {
		log.G(ctx).Infof("Checkpointing container %s in pod %s", ci.container.ID, sandbox.ID)

		task := pausedTasks[ci.container.ID]

		// Get container status for metadata
		criContainerStatus, err := c.ContainerStatus(ctx, &runtime.ContainerStatusRequest{
			ContainerId: ci.container.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get container status for %s: %w", ci.container.ID, err)
		}

		configJSON, err := json.Marshal(&crmetadata.ContainerConfig{
			ID:              ci.container.ID,
			Name:            ci.container.Name,
			RootfsImageName: criContainerStatus.GetStatus().GetImage().GetImage(),
			RootfsImageRef:  criContainerStatus.GetStatus().GetImageRef(),
			OCIRuntime:      ci.info.Runtime.Name,
			RootfsImage:     criContainerStatus.GetStatus().GetImage().GetImage(),
			CheckpointedAt:  time.Now(),
			CreatedTime:     ci.info.CreatedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("generating container config JSON for %s failed: %w", ci.container.ID, err)
		}

		// Create container directory: <containerID>-<containerName>
		containerDirName := fmt.Sprintf("%s-%s", ci.container.ID, ci.container.Name)
		containerDir := filepath.Join(mountPoint, containerDirName)
		if err := os.MkdirAll(containerDir, 0o700); err != nil {
			return nil, fmt.Errorf("failed to create container directory %s: %w", containerDir, err)
		}

		// Checkpoint the container's task
		// All containers are already paused, so CRIU should not try to freeze again.
		img, err := task.Checkpoint(ctx, []client.CheckpointTaskOpts{
			withPodCheckpointOpts(ci.info.Runtime.Name, c.getContainerRootDir(ci.container.ID), leaveRunning),
		}...)
		if err != nil {
			return nil, fmt.Errorf("checkpointing container %s failed: %w", ci.container.ID, err)
		}

		// Extract checkpoint data from the OCI index
		var (
			index        v1.Index
			targetDesc   = img.Target()
			contentStore = img.ContentStore()
		)

		// Remove checkpoint image from containerd's image store once done
		defer c.client.ImageService().Delete(ctx, img.Metadata().Name)

		rawIndex, err := content.ReadBlob(ctx, contentStore, targetDesc)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve checkpoint index for %s: %w", ci.container.ID, err)
		}
		if err = json.Unmarshal(rawIndex, &index); err != nil {
			return nil, fmt.Errorf("failed to unmarshal checkpoint index for %s: %w", ci.container.ID, err)
		}

		// Write metadata files
		if err := os.WriteFile(filepath.Join(containerDir, crmetadata.ConfigDumpFile), configJSON, 0o600); err != nil {
			return nil, fmt.Errorf("failed to write config.dump for %s: %w", ci.container.ID, err)
		}

		containerStatus, err := json.Marshal(criContainerStatus.GetStatus())
		if err != nil {
			return nil, fmt.Errorf("failed to marshal container status for %s: %w", ci.container.ID, err)
		}
		if err := os.WriteFile(filepath.Join(containerDir, crmetadata.StatusDumpFile), containerStatus, 0o600); err != nil {
			return nil, fmt.Errorf("failed to write status.dump for %s: %w", ci.container.ID, err)
		}

		// Copy containerd status file
		statusSrc := filepath.Join(c.getContainerRootDir(ci.container.ID), crmetadata.StatusFile)
		if _, err := os.Stat(statusSrc); err == nil {
			if err := c.os.CopyFile(statusSrc, filepath.Join(containerDir, crmetadata.StatusFile), 0o600); err != nil {
				return nil, fmt.Errorf("failed to copy status file for %s: %w", ci.container.ID, err)
			}
		}

		// Copy CRIU stats dump
		statsSrc := filepath.Join(c.getContainerRootDir(ci.container.ID), stats.StatsDump)
		if _, err := os.Stat(statsSrc); err == nil {
			if err := c.os.CopyFile(statsSrc, filepath.Join(containerDir, stats.StatsDump), 0o600); err != nil {
				return nil, fmt.Errorf("failed to copy stats-dump for %s: %w", ci.container.ID, err)
			}
		}

		// Copy CRIU dump log (optional)
		dumpLogSrc := filepath.Join(c.getContainerRootDir(ci.container.ID), crmetadata.DumpLogFile)
		if _, err := os.Stat(dumpLogSrc); err == nil {
			if err := c.os.CopyFile(dumpLogSrc, filepath.Join(containerDir, crmetadata.DumpLogFile), 0o600); err != nil {
				log.G(ctx).Warnf("Failed to copy dump.log for %s: %v", ci.container.ID, err)
			}
		}

		// Save the existing container log file
		if logPath := criContainerStatus.GetStatus().GetLogPath(); logPath != "" {
			if _, err := c.os.Stat(logPath); err == nil {
				if err := c.os.CopyFile(logPath, filepath.Join(containerDir, "container.log"), 0o600); err != nil {
					log.G(ctx).Warnf("Failed to copy container log for %s: %v", ci.container.ID, err)
				}
			}
		}

		// Extract checkpoint blobs from the OCI index
		for _, manifest := range index.Manifests {
			switch manifest.MediaType {
			case images.MediaTypeContainerd1Checkpoint:
				if err := writeCriuCheckpointData(ctx, contentStore, manifest, containerDir); err != nil {
					return nil, fmt.Errorf("failed to write CRIU checkpoint data for %s: %w", ci.container.ID, err)
				}
			case v1.MediaTypeImageLayerGzip:
				if err := writeRootFsDiffTar(ctx, contentStore, manifest, containerDir); err != nil {
					return nil, fmt.Errorf("failed to write rootfs diff for %s: %w", ci.container.ID, err)
				}
			case images.MediaTypeContainerd1CheckpointConfig:
				if err := writeSpecDumpFile(ctx, contentStore, manifest, containerDir); err != nil {
					return nil, fmt.Errorf("failed to write spec.dump for %s: %w", ci.container.ID, err)
				}
			}
		}

		log.G(ctx).Infof("Checkpointed container %s into %s", ci.container.ID, containerDirName)
		containerNamesMap[ci.container.Name] = containerDirName
	}

	// Phase 3: Write pod metadata
	cpAnnotations := getContainerdCheckpointAnnotations()
	cpAnnotations[crmetadata.CheckpointAnnotationPod] = sandboxConfig.GetMetadata().GetName()
	cpAnnotations[crmetadata.CheckpointAnnotationPodID] = sandbox.ID
	cpAnnotations[crmetadata.CheckpointAnnotationNamespace] = sandboxConfig.GetMetadata().GetNamespace()
	cpAnnotations[crmetadata.CheckpointAnnotationPodUID] = sandboxConfig.GetMetadata().GetUid()

	podOptions := &crmetadata.CheckpointedPodOptions{
		Version:     1,
		Containers:  containerNamesMap,
		Annotations: cpAnnotations,
	}

	if _, err := crmetadata.WriteJSONFile(podOptions, mountPoint, crmetadata.PodOptionsFile); err != nil {
		return nil, fmt.Errorf("failed to write pod options: %w", err)
	}

	// Write pod sandbox config
	podCfgJSON, err := json.Marshal(sandboxConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pod sandbox config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(mountPoint, crmetadata.PodDumpFile), podCfgJSON, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write pod sandbox config: %w", err)
	}

	log.G(ctx).Infof("Wrote pod metadata with %d containers", len(containerNamesMap))

	// Phase 4: Create tar archive
	tar := archive.Diff(ctx, "", mountPoint)

	outFile, err := os.OpenFile(r.GetPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkpoint archive %s: %w", r.GetPath(), err)
	}
	defer outFile.Close()

	if _, err = io.Copy(outFile, tar); err != nil {
		return nil, fmt.Errorf("failed to write checkpoint archive: %w", err)
	}
	if err := tar.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize checkpoint archive: %w", err)
	}

	podCheckpointTimer.UpdateSince(start)

	log.G(ctx).Infof("Wrote pod checkpoint archive to %s for sandbox %s (%d containers)", r.GetPath(), sandbox.ID, len(containerNamesMap))

	return &runtime.CheckpointPodResponse{}, nil
}

// containerInfo pairs a container with its containerd info for convenience.
type containerInfo struct {
	container containerstore.Container
	info      containers.Container
}

// withPodCheckpointOpts returns checkpoint options for pod-level checkpointing.
// Unlike container-level forensic checkpointing, pod checkpoints may stop
// the containers (exit=true) when leaveRunning is false.
func withPodCheckpointOpts(rt, rootDir string, leaveRunning bool) client.CheckpointTaskOpts {
	return func(r *client.CheckpointTaskInfo) error {
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

// getContainerdCheckpointAnnotations gathers system metadata annotations.
func getContainerdCheckpointAnnotations() map[string]string {
	ann := make(map[string]string)

	ann[crmetadata.CheckpointAnnotationEngine] = "containerd"
	ann[crmetadata.CheckpointAnnotationEngineVersion] = version.Version
	ann[crmetadata.CheckpointAnnotationHostArch] = goruntime.GOARCH

	criuVersion, err := utils.GetCriuVersion()
	if err == nil {
		ann[crmetadata.CheckpointAnnotationCriuVersion] = strconv.Itoa(criuVersion)
	}

	var utsname unix.Utsname
	if err := unix.Uname(&utsname); err == nil {
		release := make([]byte, 0, len(utsname.Release))
		for _, b := range utsname.Release {
			if b == 0 {
				break
			}
			release = append(release, byte(b))
		}
		ann[crmetadata.CheckpointAnnotationHostKernel] = string(release)
	}

	cgroupVersion := "v1"
	if opts.IsCgroup2UnifiedMode() {
		cgroupVersion = "v2"
	}
	ann[crmetadata.CheckpointAnnotationCgroupVersion] = cgroupVersion

	if file, err := os.Open("/etc/os-release"); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if after, ok := strings.CutPrefix(line, "NAME="); ok {
				ann[crmetadata.CheckpointAnnotationDistributionName] = strings.Trim(after, `"`)
			} else if after, ok := strings.CutPrefix(line, "VERSION_ID="); ok {
				ann[crmetadata.CheckpointAnnotationDistributionVersion] = strings.Trim(after, `"`)
			} else if after, ok := strings.CutPrefix(line, "VERSION="); ok {
				if _, exists := ann[crmetadata.CheckpointAnnotationDistributionVersion]; !exists {
					ann[crmetadata.CheckpointAnnotationDistributionVersion] = strings.Trim(after, `"`)
				}
			}
		}
	}

	return ann
}
