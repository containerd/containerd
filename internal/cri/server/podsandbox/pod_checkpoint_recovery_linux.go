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
	"os"
	"path/filepath"
	"strings"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/identifiers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
)

const (
	podCheckpointMarkerVersion = 2
	podCheckpointMarkerDir     = "pod-checkpoint-freezes"
	podCheckpointMarkerSuffix  = ".json"
	podCheckpointTaskPoll      = 10 * time.Millisecond
)

// Background recovery retry delays. Variables so tests can shorten them.
var (
	podCheckpointRecoveryRetryInitial = time.Second
	podCheckpointRecoveryRetryMax     = 5 * time.Minute
)

type podCheckpointRecoveryMarker struct {
	Version int `json:"version"`
	// Namespace is the containerd namespace of the sandbox and its containers.
	Namespace    string   `json:"namespace"`
	SandboxID    string   `json:"sandboxId"`
	CgroupParent string   `json:"cgroupParent"`
	ContainerIDs []string `json:"containerIds"`
	// OutputPath is the checkpoint output directory. Recovery removes partial
	// output from it when the checkpoint did not publish its manifest.
	OutputPath   string `json:"outputPath,omitempty"`
	TasksResumed bool   `json:"tasksResumed,omitempty"`
}

type podCheckpointRuntimeTask interface {
	ID() string
	Pause(context.Context) error
	Resume(context.Context) error
	Status(context.Context) (containerd.Status, error)
}

type podCheckpointMarkerFileOperations struct {
	link          func(string, string) error
	rename        func(string, string) error
	remove        func(string) error
	syncDirectory func(string) error
}

var defaultPodCheckpointMarkerFileOperations = podCheckpointMarkerFileOperations{
	link:          os.Link,
	rename:        os.Rename,
	remove:        os.Remove,
	syncDirectory: syncDirectory,
}

// podCheckpointKey identifies a sandbox across containerd namespaces.
func podCheckpointKey(namespace, sandboxID string) string {
	return namespace + "/" + sandboxID
}

func podCheckpointMarkerFilename(namespace, sandboxID string) string {
	return fmt.Sprintf("%x%s", sha256.Sum256([]byte(podCheckpointKey(namespace, sandboxID))), podCheckpointMarkerSuffix)
}

func (c *CheckpointService) podCheckpointMarkerDirectory() string {
	return filepath.Join(c.rootDir, podCheckpointMarkerDir)
}

func (c *CheckpointService) podCheckpointMarkerPath(namespace, sandboxID string) string {
	return filepath.Join(c.podCheckpointMarkerDirectory(), podCheckpointMarkerFilename(namespace, sandboxID))
}

func validatePodCheckpointMarker(marker podCheckpointRecoveryMarker) error {
	if marker.Version != podCheckpointMarkerVersion {
		return fmt.Errorf("pod checkpoint recovery marker version %d is unsupported; expected %d", marker.Version, podCheckpointMarkerVersion)
	}
	if marker.SandboxID == "" {
		return errors.New("pod checkpoint recovery marker has no sandbox ID")
	}
	if err := identifiers.Validate(marker.Namespace); err != nil {
		return fmt.Errorf("pod checkpoint recovery marker for sandbox %q has an invalid namespace: %w", marker.SandboxID, err)
	}
	if marker.OutputPath != "" && (!filepath.IsAbs(marker.OutputPath) || filepath.Clean(marker.OutputPath) != marker.OutputPath) {
		return fmt.Errorf("pod checkpoint recovery marker for sandbox %q has an invalid output path %q", marker.SandboxID, marker.OutputPath)
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

func (c *CheckpointService) writePodCheckpointMarker(marker podCheckpointRecoveryMarker) (bool, error) {
	return c.writePodCheckpointMarkerWithFileOperations(marker, defaultPodCheckpointMarkerFileOperations)
}

// beginPodCheckpoint must be called while holding the in-progress reservation
// for the marker's sandbox.
func (c *CheckpointService) beginPodCheckpoint(ctx context.Context, freezer podCgroupFreezer, marker podCheckpointRecoveryMarker) (bool, error) {
	// A previous checkpoint of this sandbox may have failed to recover. Finish
	// that recovery first, since it may still own a freeze, paused tasks or
	// temporary images.
	if err := c.recoverStalePodCheckpointMarker(ctx, marker.Namespace, marker.SandboxID); err != nil {
		return false, fmt.Errorf("failed to recover previous checkpoint of sandbox %q: %w", marker.SandboxID, err)
	}
	// Reject an existing freeze before publishing a marker that would cause
	// restart recovery to thaw it.
	state, err := freezer.State(ctx)
	if err != nil {
		return false, err
	}
	if state != podCgroupStateThawed {
		return false, fmt.Errorf("refusing to checkpoint a pod whose cgroup is %s: %w", state, errdefs.ErrFailedPrecondition)
	}
	return c.writePodCheckpointMarker(marker)
}

func (c *CheckpointService) recoverStalePodCheckpointMarker(ctx context.Context, namespace, sandboxID string) error {
	path := c.podCheckpointMarkerPath(namespace, sandboxID)
	marker, err := readPodCheckpointMarker(path)
	if errors.Is(err, os.ErrNotExist) {
		c.podCheckpointRecoveries.Delete(podCheckpointKey(namespace, sandboxID))
		return nil
	}
	if err != nil {
		return err
	}
	if marker.Namespace != namespace || marker.SandboxID != sandboxID {
		return fmt.Errorf("pod checkpoint recovery marker %q does not match sandbox %q", path, sandboxID)
	}
	return c.recoverPodCheckpointMarker(ctx, marker)
}

// writePodCheckpointMarkerWithFileOperations returns true once the published
// marker may need recovery, even when a later durability operation fails.
func (c *CheckpointService) writePodCheckpointMarkerWithFileOperations(marker podCheckpointRecoveryMarker, operations podCheckpointMarkerFileOperations) (bool, error) {
	if err := validatePodCheckpointMarker(marker); err != nil {
		return false, err
	}
	dir := c.podCheckpointMarkerDirectory()
	if err := ensurePrivateDirectory(dir); err != nil {
		return false, fmt.Errorf("failed to prepare pod checkpoint recovery marker directory %q: %w", dir, err)
	}
	if err := syncDirectory(c.rootDir); err != nil {
		return false, fmt.Errorf("failed to persist pod checkpoint recovery marker directory in %q: %w", c.rootDir, err)
	}

	tempPath, err := c.writePodCheckpointMarkerTemp(dir, marker)
	if err != nil {
		return false, err
	}
	defer c.removePodCheckpointMarkerTemp(tempPath)

	markerPath := c.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID)
	if err := operations.link(tempPath, markerPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, fmt.Errorf("pod checkpoint recovery marker already exists for sandbox %q: %w", marker.SandboxID, errdefs.ErrAlreadyExists)
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

func (c *CheckpointService) writePodCheckpointMarkerTemp(dir string, marker podCheckpointRecoveryMarker) (_ string, retErr error) {
	data, err := json.Marshal(marker)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pod checkpoint recovery marker: %w", err)
	}
	// Register the name before a recovery scan can see it. The file may be
	// written and synced without holding markerMu once its name is protected.
	c.markerMu.Lock()
	temp, err := os.CreateTemp(dir, ".marker-tmp-")
	if err == nil {
		if c.markerTemps == nil {
			c.markerTemps = make(map[string]struct{})
		}
		c.markerTemps[temp.Name()] = struct{}{}
	}
	c.markerMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("failed to create temporary pod checkpoint recovery marker: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if retErr != nil {
			_ = temp.Close()
			c.removePodCheckpointMarkerTemp(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return "", fmt.Errorf("failed to set temporary pod checkpoint recovery marker permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return "", fmt.Errorf("failed to write temporary pod checkpoint recovery marker: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return "", fmt.Errorf("failed to persist temporary pod checkpoint recovery marker: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("failed to close temporary pod checkpoint recovery marker: %w", err)
	}
	return tempPath, nil
}

func (c *CheckpointService) removePodCheckpointMarkerTemp(path string) {
	c.markerMu.Lock()
	defer c.markerMu.Unlock()
	_ = os.Remove(path)
	delete(c.markerTemps, path)
}

func (c *CheckpointService) removePodCheckpointMarker(namespace, sandboxID string) error {
	markerPath := c.podCheckpointMarkerPath(namespace, sandboxID)
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove pod checkpoint recovery marker for sandbox %q: %w", sandboxID, err)
	}
	c.podCheckpointRecoveries.Delete(podCheckpointKey(namespace, sandboxID))
	if err := syncDirectory(c.podCheckpointMarkerDirectory()); err != nil {
		return fmt.Errorf("failed to persist removal of pod checkpoint recovery marker for sandbox %q: %w", sandboxID, err)
	}
	return nil
}

// markPodCheckpointResumed retains image cleanup ownership without authorizing
// recovery to thaw or resume tasks that may since have been paused elsewhere.
func (c *CheckpointService) markPodCheckpointResumed(marker podCheckpointRecoveryMarker) error {
	return c.markPodCheckpointResumedWithFileOperations(marker, defaultPodCheckpointMarkerFileOperations)
}

func (c *CheckpointService) markPodCheckpointResumedWithFileOperations(marker podCheckpointRecoveryMarker, operations podCheckpointMarkerFileOperations) error {
	marker.TasksResumed = true
	tempPath, err := c.writePodCheckpointMarkerTemp(c.podCheckpointMarkerDirectory(), marker)
	if err != nil {
		return err
	}
	defer c.removePodCheckpointMarkerTemp(tempPath)
	if err := operations.rename(tempPath, c.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID)); err != nil {
		return fmt.Errorf("failed to publish resumed pod checkpoint marker for sandbox %q: %w", marker.SandboxID, err)
	}
	if err := operations.syncDirectory(c.podCheckpointMarkerDirectory()); err != nil {
		return fmt.Errorf("failed to persist resumed pod checkpoint marker for sandbox %q: %w", marker.SandboxID, err)
	}
	return nil
}

func (c *CheckpointService) finishPodCheckpointCleanup(ctx context.Context, marker podCheckpointRecoveryMarker) error {
	// Recover tasks and persist the resumed phase before output cleanup, so
	// an inaccessible output directory cannot leave the Pod frozen.
	if err := c.cleanupInterruptedCheckpointOutput(marker); err != nil {
		return err
	}
	if err := c.removeTemporaryCheckpointImages(ctx, marker.ContainerIDs); err != nil {
		return err
	}
	return c.removePodCheckpointMarker(marker.Namespace, marker.SandboxID)
}

// loadPodCheckpointMarkers returns every valid marker. Invalid entries do not
// prevent recovery of the others; they are reported in the returned error.
func (c *CheckpointService) loadPodCheckpointMarkers() ([]podCheckpointRecoveryMarker, error) {
	c.markerMu.Lock()
	defer c.markerMu.Unlock()

	dir := c.podCheckpointMarkerDirectory()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list pod checkpoint recovery markers: %w", err)
	}
	var (
		markers     []podCheckpointRecoveryMarker
		loadErrors  []error
		removedTemp bool
	)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".marker-tmp-") {
			path := filepath.Join(dir, entry.Name())
			if _, active := c.markerTemps[path]; active {
				continue
			}
			if err := os.Remove(path); err != nil {
				loadErrors = append(loadErrors, fmt.Errorf("failed to remove stale temporary pod checkpoint marker %q: %w", entry.Name(), err))
				continue
			}
			removedTemp = true
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), podCheckpointMarkerSuffix) {
			loadErrors = append(loadErrors, fmt.Errorf("unexpected entry %q in pod checkpoint recovery marker directory", entry.Name()))
			continue
		}
		path := filepath.Join(dir, entry.Name())
		marker, err := readPodCheckpointMarker(path)
		if err != nil {
			loadErrors = append(loadErrors, err)
			continue
		}
		if entry.Name() != podCheckpointMarkerFilename(marker.Namespace, marker.SandboxID) {
			loadErrors = append(loadErrors, fmt.Errorf("pod checkpoint recovery marker %q does not match sandbox %q", path, marker.SandboxID))
			continue
		}
		markers = append(markers, marker)
	}
	if removedTemp {
		if err := syncDirectory(dir); err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("failed to persist cleanup of temporary pod checkpoint markers: %w", err))
		}
	}
	return markers, errors.Join(loadErrors...)
}

func readPodCheckpointMarker(path string) (podCheckpointRecoveryMarker, error) {
	var marker podCheckpointRecoveryMarker
	info, err := os.Lstat(path)
	if err != nil {
		return marker, fmt.Errorf("failed to inspect pod checkpoint recovery marker %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return marker, fmt.Errorf("pod checkpoint recovery marker %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return marker, fmt.Errorf("failed to read pod checkpoint recovery marker %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return marker, fmt.Errorf("failed to decode pod checkpoint recovery marker %q: %w", path, err)
	}
	if err := validatePodCheckpointMarker(marker); err != nil {
		return marker, fmt.Errorf("invalid pod checkpoint recovery marker %q: %w", path, err)
	}
	return marker, nil
}

// podCheckpointTaskRecovery records requests made by this checkpoint which may
// still need undoing. Successful recovery removes them so retries cannot undo
// a later freeze or pause made by someone else.
type podCheckpointTaskRecovery struct {
	freezer podCgroupFreezer
	tasks   []podCheckpointRuntimeTask
	// unloaded contains task IDs not yet resolved after daemon restart.
	unloaded []string
}

func pausePodCheckpointTasks(ctx context.Context, freezer podCgroupFreezer, tasks []podCheckpointRuntimeTask) (podCheckpointTaskRecovery, error) {
	var recovery podCheckpointTaskRecovery
	requested, err := freezer.Freeze(ctx)
	if requested {
		recovery.freezer = freezer
	}
	if err != nil {
		return recovery, fmt.Errorf("failed to freeze pod cgroup before pausing containers: %w", err)
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return recovery, fmt.Errorf("pod checkpoint canceled while pausing containers: %w", err)
		}
		status, err := task.Status(ctx)
		if err != nil {
			return recovery, fmt.Errorf("failed to inspect checkpoint container %q before pause: %w", task.ID(), err)
		}
		if status.Status != containerd.Running {
			return recovery, fmt.Errorf("checkpoint container %q has state %s before pause; expected %s", task.ID(), status.Status, containerd.Running)
		}
		// A failed Pause RPC may already have paused the task.
		recovery.tasks = append(recovery.tasks, task)
		if err := task.Pause(ctx); err != nil {
			return recovery, fmt.Errorf("failed to pause checkpoint container %q: %w", task.ID(), err)
		}
		status, err = task.Status(ctx)
		if err != nil {
			return recovery, fmt.Errorf("failed to verify paused checkpoint container %q: %w", task.ID(), err)
		}
		if status.Status != containerd.Paused {
			return recovery, fmt.Errorf("checkpoint container %q has state %s after pause; expected %s", task.ID(), status.Status, containerd.Paused)
		}
	}
	if err := freezer.Thaw(ctx); err != nil {
		return recovery, fmt.Errorf("failed to thaw pod cgroup after pausing containers: %w", err)
	}
	recovery.freezer = nil
	return recovery, nil
}

func (r *podCheckpointTaskRecovery) thaw(ctx context.Context) error {
	if r.freezer == nil {
		return nil
	}
	if err := r.freezer.Thaw(ctx); err != nil {
		return fmt.Errorf("failed to thaw pod cgroup during checkpoint recovery: %w", err)
	}
	r.freezer = nil
	return nil
}

func (r *podCheckpointTaskRecovery) recover(ctx context.Context) error {
	recoveryErrors := []error{r.thaw(ctx)}
	pending := r.tasks[:0]
	for _, task := range r.tasks {
		if err := resumePodCheckpointTask(ctx, task); err != nil {
			recoveryErrors = append(recoveryErrors, err)
			pending = append(pending, task)
		}
	}
	r.tasks = pending
	return errors.Join(recoveryErrors...)
}

func resumePodCheckpointTask(ctx context.Context, task podCheckpointRuntimeTask) error {
	ticker := time.NewTicker(podCheckpointTaskPoll)
	defer ticker.Stop()
	for {
		status, err := task.Status(ctx)
		if errdefs.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to inspect checkpoint container %q during recovery: %w", task.ID(), err)
		}
		switch status.Status {
		case containerd.Running, containerd.Stopped:
			// An exited task no longer needs resuming. Do not let it retain
			// a recovery marker and prevent CRI from starting.
			return nil
		case containerd.Paused:
			if err := task.Resume(ctx); err != nil {
				return fmt.Errorf("failed to resume checkpoint container %q: %w", task.ID(), err)
			}
			status, err = task.Status(ctx)
			if errdefs.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed to verify resumed checkpoint container %q: %w", task.ID(), err)
			}
			if status.Status != containerd.Running && status.Status != containerd.Stopped {
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

// Recover resumes tasks left paused by an interrupted checkpoint before CRI
// reloads its stores. Recovery is controller-owned and uses only durable marker
// data plus containerd's public container and task APIs. It attempts every
// marker and returns the joined errors; failed markers stay for a later retry.
func (c *CheckpointService) Recover(ctx context.Context) error {
	markers, err := c.loadPodCheckpointMarkers()
	recoveryErrors := []error{err}
	for _, marker := range markers {
		// A checkpoint in progress owns its marker and recovers it itself.
		key := podCheckpointKey(marker.Namespace, marker.SandboxID)
		if _, busy := c.podCheckpointsInProgress.LoadOrStore(key, struct{}{}); busy {
			continue
		}
		// The owner may have updated or removed the marker since it was
		// listed. Read its current phase while holding the reservation.
		err := c.recoverStalePodCheckpointMarker(ctx, marker.Namespace, marker.SandboxID)
		c.podCheckpointsInProgress.Delete(key)
		if err != nil {
			recoveryErrors = append(recoveryErrors, err)
		}
	}
	return errors.Join(recoveryErrors...)
}

// RecoverInBackground retries Recover until it succeeds. Only one retry loop
// runs at a time; a request made while it runs causes another pass.
func (c *CheckpointService) RecoverInBackground() {
	c.recoveryMu.Lock()
	defer c.recoveryMu.Unlock()
	c.recoveryPending = true
	if c.recoveryRunning {
		return
	}
	c.recoveryRunning = true
	go c.retryRecovery()
}

func (c *CheckpointService) retryRecovery() {
	delay := podCheckpointRecoveryRetryInitial
	for {
		time.Sleep(delay)
		c.recoveryMu.Lock()
		c.recoveryPending = false
		c.recoveryMu.Unlock()

		err := c.Recover(context.Background())

		c.recoveryMu.Lock()
		if err == nil && !c.recoveryPending {
			c.recoveryRunning = false
			c.recoveryMu.Unlock()
			return
		}
		c.recoveryMu.Unlock()
		if err != nil {
			log.L.WithError(err).Errorf("Failed to recover interrupted pod checkpoints; retrying in %s", delay)
			delay = min(delay*2, podCheckpointRecoveryRetryMax)
		} else {
			delay = podCheckpointRecoveryRetryInitial
		}
	}
}

// recoverPodCheckpointMarker must be called while holding the in-progress
// reservation for the marker's sandbox.
func (c *CheckpointService) recoverPodCheckpointMarker(ctx context.Context, marker podCheckpointRecoveryMarker) error {
	ctx = namespaces.WithNamespace(ctx, marker.Namespace)
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("sandbox", marker.SandboxID))
	if marker.TasksResumed {
		return c.finishPodCheckpointCleanup(ctx, marker)
	}
	key := podCheckpointKey(marker.Namespace, marker.SandboxID)
	cached, ok := c.podCheckpointRecoveries.Load(key)
	var recovery *podCheckpointTaskRecovery
	if ok {
		recovery = cached.(*podCheckpointTaskRecovery)
	} else {
		var err error
		recovery, err = loadPodCheckpointRecovery(marker)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return c.recoverMissingCheckpointCgroup(ctx, marker)
			}
			return err
		}
		c.podCheckpointRecoveries.Store(key, recovery)
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, loadContainerTimeout)
	err := c.recoverPodCheckpointTasks(recoveryCtx, recovery)
	cancel()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c.recoverMissingCheckpointCgroup(ctx, marker)
		}
		return fmt.Errorf("failed to resume containers after interrupted checkpoint of sandbox %q: %w", marker.SandboxID, err)
	}
	if err := c.markPodCheckpointResumed(marker); err != nil {
		return err
	}
	if err := c.finishPodCheckpointCleanup(ctx, marker); err != nil {
		return err
	}
	log.G(ctx).Info("Recovered interrupted pod checkpoint")
	return nil
}

// loadPodCheckpointRecovery reconstructs pending runtime work once after a
// daemon restart. Callers retain the result until the marker is removed, so a
// failed phase write cannot replay a completed thaw or resume in this process.
func loadPodCheckpointRecovery(marker podCheckpointRecoveryMarker) (*podCheckpointTaskRecovery, error) {
	freezer, err := loadPodCgroupFreezer(marker.CgroupParent)
	if err != nil {
		return nil, fmt.Errorf("failed to load pod cgroup for checkpoint recovery of sandbox %q: %w", marker.SandboxID, err)
	}
	return &podCheckpointTaskRecovery{
		freezer:  freezer,
		unloaded: append([]string(nil), marker.ContainerIDs...),
	}, nil
}

func (c *CheckpointService) recoverPodCheckpointTasks(ctx context.Context, recovery *podCheckpointTaskRecovery) error {
	// Thaw before looking up tasks: a blocked lookup can exhaust the context.
	// Retain the successful thaw even if a subsequent lookup fails.
	if err := recovery.thaw(ctx); err != nil {
		return err
	}

	var loadErrors []error
	pending := recovery.unloaded[:0]
	for _, containerID := range recovery.unloaded {
		container, err := c.client.LoadContainer(ctx, containerID)
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			pending = append(pending, containerID)
			loadErrors = append(loadErrors, fmt.Errorf("failed to load checkpoint recovery container %q: %w", containerID, err))
			continue
		}
		task, err := container.Task(ctx, nil)
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			pending = append(pending, containerID)
			loadErrors = append(loadErrors, fmt.Errorf("failed to load checkpoint recovery task %q: %w", containerID, err))
			continue
		}
		recovery.tasks = append(recovery.tasks, task)
	}
	recovery.unloaded = pending

	// Keep lookup and runtime failures for retry without replaying completed work.
	return errors.Join(errors.Join(loadErrors...), recovery.recover(ctx))
}

// cleanupInterruptedCheckpointOutput removes the files an interrupted
// checkpoint may have written. A published manifest marks a complete
// checkpoint, which is kept. Only names this service writes are removed, and
// only while no other checkpoint is using the directory.
func (c *CheckpointService) cleanupInterruptedCheckpointOutput(marker podCheckpointRecoveryMarker) error {
	if marker.OutputPath == "" {
		return nil
	}
	canonicalDir, err := filepath.EvalSymlinks(marker.OutputPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to resolve interrupted checkpoint output %q: %w", marker.OutputPath, err)
	}
	if canonicalDir != marker.OutputPath {
		// The path was replaced by a symlink; do not follow it.
		return nil
	}
	if _, busy := c.podCheckpointOutputsInProgress.LoadOrStore(canonicalDir, struct{}{}); busy {
		return nil
	}
	defer c.podCheckpointOutputsInProgress.Delete(canonicalDir)

	if _, err := os.Lstat(filepath.Join(canonicalDir, podCheckpointManifestFile)); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to inspect interrupted checkpoint output %q: %w", canonicalDir, err)
	}
	entries, err := os.ReadDir(canonicalDir)
	if err != nil {
		return fmt.Errorf("failed to read interrupted checkpoint output %q: %w", canonicalDir, err)
	}
	owned := map[string]struct{}{podCheckpointConfigFile: {}}
	for _, containerID := range marker.ContainerIDs {
		owned[checkpointArchiveName(containerID)] = struct{}{}
	}
	var cleanupErrors []error
	for _, entry := range entries {
		name := entry.Name()
		if _, ok := owned[name]; !ok && !strings.HasPrefix(name, checkpointArchiveTempPrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(canonicalDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove interrupted checkpoint output %q: %w", name, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func (c *CheckpointService) recoverMissingCheckpointCgroup(ctx context.Context, marker podCheckpointRecoveryMarker) error {
	ctx, cancel := context.WithTimeout(ctx, loadContainerTimeout)
	defer cancel()

	// After a reboot, container metadata can remain without tasks or cgroups.
	containerIDs := append([]string{marker.SandboxID}, marker.ContainerIDs...)
	for _, containerID := range containerIDs {
		container, err := c.client.LoadContainer(ctx, containerID)
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to load checkpoint recovery container %q: %w", containerID, err)
		}
		if _, err := container.Task(ctx, nil); err == nil {
			return fmt.Errorf("pod checkpoint recovery cgroup is missing but task %q still exists", containerID)
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to check checkpoint recovery task %q: %w", containerID, err)
		}
	}
	if err := c.markPodCheckpointResumed(marker); err != nil {
		return err
	}
	if err := c.finishPodCheckpointCleanup(ctx, marker); err != nil {
		return err
	}
	log.G(ctx).Info("Removed checkpoint recovery marker for pod without tasks")
	return nil
}

func (c *CheckpointService) removeTemporaryCheckpointImages(ctx context.Context, containerIDs []string) error {
	cleanupCtx, cancel := context.WithTimeout(ctx, loadContainerTimeout)
	defer cancel()
	for _, containerID := range containerIDs {
		image := checkpointImageName(containerID)
		if err := c.client.ImageService().Delete(cleanupCtx, image); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to remove temporary checkpoint image %q during recovery: %w", image, err)
		}
	}
	return nil
}
