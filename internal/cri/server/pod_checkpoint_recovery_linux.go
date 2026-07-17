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
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
)

const (
	podCheckpointMarkerVersion = 1
	podCheckpointMarkerDir     = "pod-checkpoint-freezes"
	podCheckpointMarkerSuffix  = ".json"
	podCheckpointTaskPoll      = 10 * time.Millisecond
)

type podCheckpointRecoveryMarker struct {
	Version      int      `json:"version"`
	SandboxID    string   `json:"sandboxId"`
	CgroupParent string   `json:"cgroupParent"`
	ContainerIDs []string `json:"containerIds"`
}

type pendingPodCheckpointRecovery struct {
	marker        podCheckpointRecoveryMarker
	freezer       podCgroupFreezer
	cgroupMissing bool
}

type podCheckpointRuntimeTask interface {
	ID() string
	Pause(context.Context) error
	Resume(context.Context) error
	Status(context.Context) (containerd.Status, error)
}

type podCheckpointMarkerFileOperations struct {
	link          func(string, string) error
	remove        func(string) error
	syncDirectory func(string) error
}

var defaultPodCheckpointMarkerFileOperations = podCheckpointMarkerFileOperations{
	link:          os.Link,
	remove:        os.Remove,
	syncDirectory: syncDirectory,
}

func requireRequestDeadline(ctx context.Context, operation string) error {
	if ctx == nil {
		return fmt.Errorf("%s requires a non-nil context with a finite deadline", operation)
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("%s requires a finite context deadline", operation)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s cannot start: %w", operation, err)
	}
	return nil
}

func podCheckpointMarkerFilename(sandboxID string) string {
	return fmt.Sprintf("%x%s", sha256.Sum256([]byte(sandboxID)), podCheckpointMarkerSuffix)
}

func (c *criService) podCheckpointMarkerDirectory() string {
	return filepath.Join(c.config.RootDir, podCheckpointMarkerDir)
}

func (c *criService) podCheckpointMarkerPath(sandboxID string) string {
	return filepath.Join(c.podCheckpointMarkerDirectory(), podCheckpointMarkerFilename(sandboxID))
}

func validatePodCheckpointMarker(marker podCheckpointRecoveryMarker) error {
	if marker.Version != podCheckpointMarkerVersion {
		return fmt.Errorf("pod checkpoint recovery marker version %d is unsupported; expected %d", marker.Version, podCheckpointMarkerVersion)
	}
	if marker.SandboxID == "" {
		return errors.New("pod checkpoint recovery marker has no sandbox ID")
	}
	if err := validatePodCgroupPath(marker.CgroupParent); err != nil {
		return fmt.Errorf("pod checkpoint recovery marker for sandbox %q has an invalid cgroup parent: %w", marker.SandboxID, err)
	}
	if len(marker.ContainerIDs) == 0 {
		return fmt.Errorf("pod checkpoint recovery marker for sandbox %q has no container IDs", marker.SandboxID)
	}
	seen := make(map[string]struct{}, len(marker.ContainerIDs))
	for i, containerID := range marker.ContainerIDs {
		if containerID == "" {
			return fmt.Errorf("pod checkpoint recovery marker for sandbox %q has an empty container ID at index %d", marker.SandboxID, i)
		}
		if _, exists := seen[containerID]; exists {
			return fmt.Errorf("pod checkpoint recovery marker for sandbox %q contains duplicate container ID %q", marker.SandboxID, containerID)
		}
		seen[containerID] = struct{}{}
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("path %q is not a real directory", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (c *criService) writePodCheckpointMarker(marker podCheckpointRecoveryMarker) (bool, error) {
	return c.writePodCheckpointMarkerWithFileOperations(marker, defaultPodCheckpointMarkerFileOperations)
}

// writePodCheckpointMarkerWithFileOperations returns true once the published
// marker may need recovery, even when a later durability operation fails.
func (c *criService) writePodCheckpointMarkerWithFileOperations(marker podCheckpointRecoveryMarker, operations podCheckpointMarkerFileOperations) (bool, error) {
	if err := validatePodCheckpointMarker(marker); err != nil {
		return false, err
	}
	dir := c.podCheckpointMarkerDirectory()
	if err := ensurePrivateDirectory(dir); err != nil {
		return false, fmt.Errorf("failed to prepare pod checkpoint recovery marker directory %q: %w", dir, err)
	}
	if err := syncDirectory(c.config.RootDir); err != nil {
		return false, fmt.Errorf("failed to persist pod checkpoint recovery marker directory %q: %w", dir, err)
	}

	data, err := json.Marshal(marker)
	if err != nil {
		return false, fmt.Errorf("failed to marshal pod checkpoint recovery marker: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".marker-tmp-")
	if err != nil {
		return false, fmt.Errorf("failed to create temporary pod checkpoint recovery marker: %w", err)
	}
	tempPath := temp.Name()
	tempClosed := false
	defer func() {
		if !tempClosed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return false, fmt.Errorf("failed to set temporary pod checkpoint recovery marker permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return false, fmt.Errorf("failed to write temporary pod checkpoint recovery marker: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return false, fmt.Errorf("failed to persist temporary pod checkpoint recovery marker: %w", err)
	}
	if err := temp.Close(); err != nil {
		return false, fmt.Errorf("failed to close temporary pod checkpoint recovery marker: %w", err)
	}
	tempClosed = true

	markerPath := c.podCheckpointMarkerPath(marker.SandboxID)
	if err := operations.link(tempPath, markerPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, fmt.Errorf("pod checkpoint recovery marker already exists for sandbox %q", marker.SandboxID)
		}
		return false, fmt.Errorf("failed to publish pod checkpoint recovery marker for sandbox %q: %w", marker.SandboxID, err)
	}
	// The published hard link owns the data now. A leftover hidden temporary
	// link is harmless and is removed by the deferred best-effort cleanup.
	_ = operations.remove(tempPath)
	if err := operations.syncDirectory(dir); err == nil {
		return true, nil
	} else {
		publishErr := fmt.Errorf("failed to persist pod checkpoint recovery marker for sandbox %q: %w", marker.SandboxID, err)
		removeErr := operations.remove(markerPath)
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			if rollbackSyncErr := operations.syncDirectory(dir); rollbackSyncErr == nil {
				return false, publishErr
			} else {
				return true, errors.Join(publishErr, fmt.Errorf("failed to persist rollback of pod checkpoint recovery marker for sandbox %q: %w", marker.SandboxID, rollbackSyncErr))
			}
		}
		return true, errors.Join(publishErr, fmt.Errorf("failed to roll back pod checkpoint recovery marker for sandbox %q: %w", marker.SandboxID, removeErr))
	}
}

func (c *criService) removePodCheckpointMarker(sandboxID string) error {
	markerPath := c.podCheckpointMarkerPath(sandboxID)
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove pod checkpoint recovery marker for sandbox %q: %w", sandboxID, err)
	}
	if err := syncDirectory(c.podCheckpointMarkerDirectory()); err != nil {
		return fmt.Errorf("failed to persist removal of pod checkpoint recovery marker for sandbox %q: %w", sandboxID, err)
	}
	return nil
}

func (c *criService) loadPodCheckpointMarkers() ([]podCheckpointRecoveryMarker, error) {
	dir := c.podCheckpointMarkerDirectory()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list pod checkpoint recovery markers: %w", err)
	}
	var markers []podCheckpointRecoveryMarker
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".marker-tmp-") {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return nil, fmt.Errorf("failed to remove stale temporary pod checkpoint marker %q: %w", entry.Name(), err)
			}
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), podCheckpointMarkerSuffix) {
			return nil, fmt.Errorf("unexpected entry %q in pod checkpoint recovery marker directory", entry.Name())
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect pod checkpoint recovery marker %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("pod checkpoint recovery marker %q is not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read pod checkpoint recovery marker %q: %w", path, err)
		}
		var marker podCheckpointRecoveryMarker
		if err := json.Unmarshal(data, &marker); err != nil {
			return nil, fmt.Errorf("failed to decode pod checkpoint recovery marker %q: %w", path, err)
		}
		if err := validatePodCheckpointMarker(marker); err != nil {
			return nil, fmt.Errorf("invalid pod checkpoint recovery marker %q: %w", path, err)
		}
		if entry.Name() != podCheckpointMarkerFilename(marker.SandboxID) {
			return nil, fmt.Errorf("pod checkpoint recovery marker %q does not match sandbox %q", path, marker.SandboxID)
		}
		markers = append(markers, marker)
	}
	if len(entries) != len(markers) {
		if err := syncDirectory(dir); err != nil {
			return nil, fmt.Errorf("failed to persist cleanup of temporary pod checkpoint markers: %w", err)
		}
	}
	return markers, nil
}

func pausePodCheckpointTasks(ctx context.Context, freezer podCgroupFreezer, tasks []podCheckpointRuntimeTask) error {
	state, err := freezer.State(ctx)
	if err != nil {
		return err
	}
	if state != podCgroupStateThawed {
		return fmt.Errorf("refusing to checkpoint a pod whose cgroup is %s", state)
	}
	if err := freezer.Freeze(ctx); err != nil {
		return fmt.Errorf("failed to freeze pod cgroup before pausing containers: %w", err)
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pod checkpoint canceled while pausing containers: %w", err)
		}
		if err := task.Pause(ctx); err != nil {
			return fmt.Errorf("failed to pause checkpoint container %q: %w", task.ID(), err)
		}
		status, err := task.Status(ctx)
		if err != nil {
			return fmt.Errorf("failed to verify paused checkpoint container %q: %w", task.ID(), err)
		}
		if status.Status != containerd.Paused {
			return fmt.Errorf("checkpoint container %q has state %s after pause; expected %s", task.ID(), status.Status, containerd.Paused)
		}
	}
	if err := freezer.Thaw(ctx); err != nil {
		return fmt.Errorf("failed to thaw pod cgroup after pausing containers: %w", err)
	}
	return nil
}

func recoverPodCheckpointTasks(ctx context.Context, freezer podCgroupFreezer, tasks []podCheckpointRuntimeTask) error {
	var recoveryErrors []error
	if err := freezer.Thaw(ctx); err != nil {
		recoveryErrors = append(recoveryErrors, fmt.Errorf("failed to thaw pod cgroup during checkpoint recovery: %w", err))
	}
	for _, task := range tasks {
		if err := resumePodCheckpointTask(ctx, task); err != nil {
			recoveryErrors = append(recoveryErrors, err)
		}
	}
	return errors.Join(recoveryErrors...)
}

func resumePodCheckpointTask(ctx context.Context, task podCheckpointRuntimeTask) error {
	ticker := time.NewTicker(podCheckpointTaskPoll)
	defer ticker.Stop()
	for {
		status, err := task.Status(ctx)
		if err != nil {
			return fmt.Errorf("failed to inspect checkpoint container %q during recovery: %w", task.ID(), err)
		}
		switch status.Status {
		case containerd.Running:
			return nil
		case containerd.Paused:
			if err := task.Resume(ctx); err != nil {
				return fmt.Errorf("failed to resume checkpoint container %q: %w", task.ID(), err)
			}
			status, err = task.Status(ctx)
			if err != nil {
				return fmt.Errorf("failed to verify resumed checkpoint container %q: %w", task.ID(), err)
			}
			if status.Status != containerd.Running {
				return fmt.Errorf("checkpoint container %q has state %s after resume; expected %s", task.ID(), status.Status, containerd.Running)
			}
			return nil
		case containerd.Pausing:
			select {
			case <-ctx.Done():
				return fmt.Errorf("waiting for checkpoint container %q to finish pausing: %w", task.ID(), ctx.Err())
			case <-ticker.C:
			}
		default:
			return fmt.Errorf("checkpoint container %q cannot be recovered from state %s", task.ID(), status.Status)
		}
	}
}

func (c *criService) preparePodCheckpointRecovery(ctx context.Context) ([]pendingPodCheckpointRecovery, error) {
	markers, err := c.loadPodCheckpointMarkers()
	if err != nil {
		return nil, err
	}
	pending := make([]pendingPodCheckpointRecovery, 0, len(markers))
	for _, marker := range markers {
		freezer, err := loadPodCgroupFreezer(marker.CgroupParent)
		if err != nil {
			return nil, fmt.Errorf("failed to load pod cgroup for checkpoint recovery of sandbox %q: %w", marker.SandboxID, err)
		}
		recoveryCtx, cancel := context.WithTimeout(ctx, loadContainerTimeout)
		err = freezer.Thaw(recoveryCtx)
		cancel()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				pending = append(pending, pendingPodCheckpointRecovery{marker: marker, cgroupMissing: true})
				for _, containerID := range marker.ContainerIDs {
					c.podCheckpointRecoveryContainers.Store(containerID, struct{}{})
				}
				log.G(ctx).WithField("sandbox", marker.SandboxID).Warn("Found interrupted pod checkpoint whose cgroup is already gone")
				continue
			}
			return nil, fmt.Errorf("failed to thaw pod cgroup before recovering sandbox %q: %w", marker.SandboxID, err)
		}
		pending = append(pending, pendingPodCheckpointRecovery{marker: marker, freezer: freezer})
		for _, containerID := range marker.ContainerIDs {
			c.podCheckpointRecoveryContainers.Store(containerID, struct{}{})
		}
		log.G(ctx).WithField("sandbox", marker.SandboxID).Warn("Found interrupted pod checkpoint; thawed parent cgroup")
	}
	return pending, nil
}

func (c *criService) finishPodCheckpointRecovery(ctx context.Context, pending []pendingPodCheckpointRecovery) error {
	for _, recovery := range pending {
		if recovery.cgroupMissing {
			if _, err := c.sandboxStore.Get(recovery.marker.SandboxID); err == nil {
				return fmt.Errorf("pod checkpoint recovery cgroup is missing but sandbox %q still exists", recovery.marker.SandboxID)
			}
			if _, err := c.client.LoadContainer(ctx, recovery.marker.SandboxID); err == nil {
				return fmt.Errorf("pod checkpoint recovery cgroup is missing but sandbox container %q still exists", recovery.marker.SandboxID)
			} else if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to verify deleted checkpoint sandbox container %q: %w", recovery.marker.SandboxID, err)
			}
			for _, containerID := range recovery.marker.ContainerIDs {
				if _, err := c.containerStore.Get(containerID); err == nil {
					return fmt.Errorf("pod checkpoint recovery cgroup is missing but container %q still exists", containerID)
				}
				if _, err := c.client.LoadContainer(ctx, containerID); err == nil {
					return fmt.Errorf("pod checkpoint recovery cgroup is missing but containerd container %q still exists", containerID)
				} else if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to verify deleted checkpoint container %q: %w", containerID, err)
				}
			}
			if err := c.removePodCheckpointMarker(recovery.marker.SandboxID); err != nil {
				return err
			}
			for _, containerID := range recovery.marker.ContainerIDs {
				c.podCheckpointRecoveryContainers.Delete(containerID)
			}
			log.G(ctx).WithField("sandbox", recovery.marker.SandboxID).Info("Removed checkpoint recovery marker for deleted pod")
			continue
		}
		tasks := make([]podCheckpointRuntimeTask, 0, len(recovery.marker.ContainerIDs))
		for _, containerID := range recovery.marker.ContainerIDs {
			container, err := c.client.LoadContainer(ctx, containerID)
			if errdefs.IsNotFound(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("failed to load checkpoint recovery container %q: %w", containerID, err)
			}
			task, err := container.Task(ctx, nil)
			if errdefs.IsNotFound(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("failed to load checkpoint recovery task %q: %w", containerID, err)
			}
			tasks = append(tasks, task)
		}
		recoveryCtx, cancel := context.WithTimeout(ctx, loadContainerTimeout)
		err := recoverPodCheckpointTasks(recoveryCtx, recovery.freezer, tasks)
		cancel()
		if err != nil {
			return fmt.Errorf("failed to resume containers after interrupted checkpoint of sandbox %q: %w", recovery.marker.SandboxID, err)
		}
		if err := c.removePodCheckpointMarker(recovery.marker.SandboxID); err != nil {
			return err
		}
		for _, containerID := range recovery.marker.ContainerIDs {
			c.podCheckpointRecoveryContainers.Delete(containerID)
		}
		log.G(ctx).WithField("sandbox", recovery.marker.SandboxID).Info("Recovered interrupted pod checkpoint")
	}
	return nil
}
