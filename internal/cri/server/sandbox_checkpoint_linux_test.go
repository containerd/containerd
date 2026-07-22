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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

func podRPCTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestPodCheckpointAndRestoreRequireDeadline(t *testing.T) {
	c := newTestCRIService()
	_, err := c.CheckpointPod(context.Background(), &runtime.CheckpointPodRequest{
		PodSandboxId: "sandbox-1",
		OutputPath:   t.TempDir(),
		ContainerIds: []string{"container-1"},
	})
	require.ErrorContains(t, err, "finite context deadline")

	_, err = c.RestorePod(context.Background(), &runtime.RestorePodRequest{})
	require.ErrorContains(t, err, "finite context deadline")
}

func TestPodCheckpointAndRestoreRejectUnsupportedOptions(t *testing.T) {
	c := newTestCRIService()
	outputPath := t.TempDir()

	_, err := c.CheckpointPod(podRPCTestContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: "sandbox-1",
		OutputPath:   outputPath,
		ContainerIds: []string{"container-1"},
		Options: map[string]string{
			"example.runtime/checkpoint-mode": "fast",
		},
	})
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
	require.ErrorContains(t, err, `pod checkpoint options ["example.runtime/checkpoint-mode"] are not supported`)
	entries, readErr := os.ReadDir(outputPath)
	require.NoError(t, readErr)
	assert.Empty(t, entries)

	_, err = c.RestorePod(podRPCTestContext(t), &runtime.RestorePodRequest{
		CheckpointPath: "/does/not/exist",
		Options: map[string]string{
			"example.runtime/restore-mode": "lazy",
		},
	})
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
	require.ErrorContains(t, err, `pod restore options ["example.runtime/restore-mode"] are not supported`)
}

func TestValidateCheckpointOutputPath(t *testing.T) {
	base := t.TempDir()
	emptyDir := filepath.Join(base, "empty")
	require.NoError(t, os.Mkdir(emptyDir, 0o700))
	nonEmptyDir := filepath.Join(base, "nonempty")
	require.NoError(t, os.Mkdir(nonEmptyDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(nonEmptyDir, "existing"), []byte("keep"), 0o600))
	regularFile := filepath.Join(base, "file")
	require.NoError(t, os.WriteFile(regularFile, nil, 0o600))
	symlink := filepath.Join(base, "symlink")
	require.NoError(t, os.Symlink(emptyDir, symlink))

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "empty", path: "", wantErr: "output path is required"},
		{name: "relative", path: "checkpoint", wantErr: "must be absolute"},
		{name: "missing", path: filepath.Join(base, "missing"), wantErr: "existing directory"},
		{name: "regular file", path: regularFile, wantErr: "is not a directory"},
		{name: "symlink", path: symlink, wantErr: "must not be a symbolic link"},
		{name: "non-empty", path: nonEmptyDir, wantErr: "must be empty"},
		{name: "valid", path: emptyDir},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCheckpointOutputPath(test.path)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestCheckpointPodSandboxNotFound(t *testing.T) {
	c := newTestCRIService()
	outputPath := t.TempDir()

	_, err := c.CheckpointPod(podRPCTestContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: "nonexistent-sandbox",
		OutputPath:   outputPath,
		ContainerIds: []string{"container-1"},
	})
	require.ErrorContains(t, err, "failed to find sandbox")
	_, checkpointInProgress := c.podCheckpointsInProgress.Load("nonexistent-sandbox")
	assert.False(t, checkpointInProgress)
	entries, readErr := os.ReadDir(outputPath)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestCheckpointPodRejectsNonEmptyOutputWithoutCleaningIt(t *testing.T) {
	c := newTestCRIService()
	outputPath := t.TempDir()
	existing := filepath.Join(outputPath, "existing")
	require.NoError(t, os.WriteFile(existing, []byte("keep"), 0o600))

	_, err := c.CheckpointPod(podRPCTestContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: "sandbox-1",
		OutputPath:   outputPath,
	})
	require.ErrorContains(t, err, "must be empty")
	data, readErr := os.ReadFile(existing)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("keep"), data)
}

func TestCheckpointPodRejectsNoContainers(t *testing.T) {
	c := newTestCRIService()
	sandboxConfig := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "uid-1",
		},
	}
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     "sandbox-1",
			Name:   "test-pod",
			Config: sandboxConfig,
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	require.NoError(t, c.sandboxStore.Add(sb))

	outputPath := t.TempDir()
	_, err := c.CheckpointPod(podRPCTestContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: "sandbox-1",
		OutputPath:   outputPath,
	})
	require.ErrorContains(t, err, "at least one container ID is required")
	entries, readErr := os.ReadDir(outputPath)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestCheckpointPodRejectsStoppedSandbox(t *testing.T) {
	c := newTestCRIService()
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     "sandbox-1",
			Name:   "test-pod",
			Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "test-pod"}},
		},
		sandboxstore.Status{State: sandboxstore.StateNotReady},
	)
	require.NoError(t, c.sandboxStore.Add(sb))

	_, err := c.CheckpointPod(podRPCTestContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: "sandbox-1",
		OutputPath:   t.TempDir(),
		ContainerIds: []string{"container-1"},
	})
	require.ErrorContains(t, err, "must be running")
}

func addCheckpointContainer(t *testing.T, c *criService, id, sandboxID, name string, status containerstore.Status) {
	t.Helper()
	cntr, err := containerstore.NewContainer(containerstore.Metadata{
		ID:        id,
		Name:      "runtime-name-" + id,
		SandboxID: sandboxID,
		Config: &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{Name: name},
		},
	}, containerstore.WithFakeStatus(status))
	require.NoError(t, err)
	require.NoError(t, c.containerStore.Add(cntr))
}

func TestSelectCheckpointContainers(t *testing.T) {
	c := newTestCRIService()
	addCheckpointContainer(t, c, "container-a", "sandbox-1", "app", containerstore.Status{StartedAt: 1})
	addCheckpointContainer(t, c, "container-b", "sandbox-1", "sidecar", containerstore.Status{StartedAt: 1})
	addCheckpointContainer(t, c, "container-stopped", "sandbox-1", "stopped", containerstore.Status{StartedAt: 1, FinishedAt: 2})
	addCheckpointContainer(t, c, "container-other", "sandbox-2", "other", containerstore.Status{StartedAt: 1})
	addCheckpointContainer(t, c, "container-duplicate-name", "sandbox-1", "app", containerstore.Status{StartedAt: 1})
	addCheckpointContainer(t, c, "container-no-name", "sandbox-1", "", containerstore.Status{StartedAt: 1})

	containers, err := c.selectCheckpointContainers("sandbox-1", []string{"container-b", "container-a"})
	require.NoError(t, err)
	require.Len(t, containers, 2)
	assert.Equal(t, "container-b", containers[0].container.ID)
	assert.Equal(t, "sidecar", containers[0].manifest.Name)
	assert.Equal(t, checkpointArchiveName("container-b"), containers[0].manifest.Archive)
	assert.NotContains(t, containers[0].manifest.Archive, "sidecar")
	assert.Less(t, len(containers[0].manifest.Archive), 128)
	assert.Equal(t, "container-a", containers[1].container.ID)

	tests := []struct {
		name         string
		containerIDs []string
		wantErr      string
	}{
		{name: "empty list", wantErr: "at least one container ID"},
		{name: "empty ID", containerIDs: []string{""}, wantErr: "at index 0 is empty"},
		{name: "duplicate ID", containerIDs: []string{"container-a", "container-a"}, wantErr: "is duplicated"},
		{name: "missing", containerIDs: []string{"missing"}, wantErr: "failed to find checkpoint container"},
		{name: "wrong sandbox", containerIDs: []string{"container-other"}, wantErr: "belongs to sandbox"},
		{name: "not running", containerIDs: []string{"container-stopped"}, wantErr: "must be running"},
		{name: "missing metadata name", containerIDs: []string{"container-no-name"}, wantErr: "has no CRI metadata name"},
		{name: "duplicate metadata name", containerIDs: []string{"container-a", "container-duplicate-name"}, wantErr: "name \"app\" is duplicated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := c.selectCheckpointContainers("sandbox-1", test.containerIDs)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestCheckpointContainerRejectsConcurrentCheckpoint(t *testing.T) {
	c := newTestCRIService()
	addCheckpointContainer(t, c, "container-1", "sandbox-1", "app", containerstore.Status{StartedAt: 1})
	c.containerCheckpointsInProgress.Store("container-1", struct{}{})
	t.Cleanup(func() { c.containerCheckpointsInProgress.Delete("container-1") })

	_, err := c.CheckpointContainer(context.Background(), &runtime.CheckpointContainerRequest{ContainerId: "container-1"})
	require.ErrorContains(t, err, "already in progress")
}

func TestCheckpointPodRejectsConcurrentCheckpoint(t *testing.T) {
	c := newTestCRIService()
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     "sandbox-1",
			Name:   "test-pod",
			Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "test-pod"}},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	require.NoError(t, c.sandboxStore.Add(sb))
	c.podCheckpointsInProgress.Store("sandbox-1", struct{}{})
	t.Cleanup(func() { c.podCheckpointsInProgress.Delete("sandbox-1") })
	outputPath := t.TempDir()

	_, err := c.CheckpointPod(podRPCTestContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: "sandbox-1",
		OutputPath:   outputPath,
		ContainerIds: []string{"container-1"},
	})
	require.ErrorContains(t, err, "already in progress")
	entries, readErr := os.ReadDir(outputPath)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestCheckpointPodConcurrentGuardUsesCanonicalSandboxID(t *testing.T) {
	c := newTestCRIService()
	const (
		sandboxID     = "abcdef1234567890abcdef1234567890"
		sandboxPrefix = "abcdef123456"
	)
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     sandboxID,
			Name:   "test-pod",
			Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "test-pod"}},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	require.NoError(t, c.sandboxStore.Add(sb))
	c.podCheckpointsInProgress.Store(sandboxID, struct{}{})
	t.Cleanup(func() { c.podCheckpointsInProgress.Delete(sandboxID) })

	_, err := c.CheckpointPod(podRPCTestContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: sandboxPrefix,
		OutputPath:   t.TempDir(),
		ContainerIds: []string{"container-1"},
	})
	require.ErrorContains(t, err, "already in progress")
	_, stillReserved := c.podCheckpointsInProgress.Load(sandboxID)
	assert.True(t, stillReserved)
}

func TestReservePodCheckpointOutputCanonicalizesParentSymlinks(t *testing.T) {
	c := newTestCRIService()
	realParent := t.TempDir()
	realOutput := filepath.Join(realParent, "checkpoint")
	require.NoError(t, os.Mkdir(realOutput, 0o700))
	aliasParent := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(realParent, aliasParent))
	aliasOutput := filepath.Join(aliasParent, "checkpoint")

	release, err := c.reservePodCheckpointOutput(realOutput)
	require.NoError(t, err)
	_, err = c.reservePodCheckpointOutput(aliasOutput)
	require.ErrorContains(t, err, "already in use")
	release()

	release, err = c.reservePodCheckpointOutput(aliasOutput)
	require.NoError(t, err)
	release()
}

func TestReserveContainerCheckpointsReleasesPartialReservation(t *testing.T) {
	c := newTestCRIService()
	c.containerCheckpointsInProgress.Store("container-b", struct{}{})
	t.Cleanup(func() { c.containerCheckpointsInProgress.Delete("container-b") })

	_, err := c.reserveContainerCheckpoints([]string{"container-a", "container-b", "container-c"})
	require.ErrorContains(t, err, "container-b")
	_, reserved := c.containerCheckpointsInProgress.Load("container-a")
	assert.False(t, reserved)
	_, reserved = c.containerCheckpointsInProgress.Load("container-b")
	assert.True(t, reserved)
	_, reserved = c.containerCheckpointsInProgress.Load("container-c")
	assert.False(t, reserved)
}

func TestCheckpointPodReservesAllContainersBeforeWritingOutput(t *testing.T) {
	c := newTestCRIService()
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     "sandbox-1",
			Name:   "test-pod",
			Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "test-pod"}},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	require.NoError(t, c.sandboxStore.Add(sb))
	addCheckpointContainer(t, c, "container-a", "sandbox-1", "app", containerstore.Status{StartedAt: 1})
	addCheckpointContainer(t, c, "container-b", "sandbox-1", "sidecar", containerstore.Status{StartedAt: 1})
	c.containerCheckpointsInProgress.Store("container-b", struct{}{})
	t.Cleanup(func() { c.containerCheckpointsInProgress.Delete("container-b") })
	outputPath := t.TempDir()

	_, err := c.CheckpointPod(podRPCTestContext(t), &runtime.CheckpointPodRequest{
		PodSandboxId: "sandbox-1",
		OutputPath:   outputPath,
		ContainerIds: []string{"container-a", "container-b"},
	})
	require.ErrorContains(t, err, "container-b")
	entries, readErr := os.ReadDir(outputPath)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
	_, reserved := c.containerCheckpointsInProgress.Load("container-a")
	assert.False(t, reserved)
	_, reserved = c.containerCheckpointsInProgress.Load("container-b")
	assert.True(t, reserved)
	_, podReserved := c.podCheckpointsInProgress.Load("sandbox-1")
	assert.False(t, podReserved)
}

func TestCheckpointPodCanceledContextLeavesEmptyOutput(t *testing.T) {
	c := newTestCRIService()
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     "sandbox-1",
			Name:   "test-pod",
			Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "test-pod"}},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	require.NoError(t, c.sandboxStore.Add(sb))
	addCheckpointContainer(t, c, "container-1", "sandbox-1", "app", containerstore.Status{StartedAt: 1})
	outputPath := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()

	_, err := c.CheckpointPod(ctx, &runtime.CheckpointPodRequest{
		PodSandboxId: "sandbox-1",
		OutputPath:   outputPath,
		ContainerIds: []string{"container-1"},
	})
	require.ErrorIs(t, err, context.Canceled)
	entries, readErr := os.ReadDir(outputPath)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func writeRestoreCheckpointEntries(t *testing.T, containers ...podCheckpointManifestContainer) string {
	t.Helper()
	checkpointPath := t.TempDir()
	sandboxConfig := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{Name: "pod", Namespace: "default", Uid: "checkpoint-uid"},
	}
	configData, err := json.Marshal(sandboxConfig)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(checkpointPath, podCheckpointConfigFile), configData, 0o600))
	manifest := podCheckpointManifest{
		Version:    podCheckpointManifestVersion,
		SandboxID:  "sandbox-original",
		Containers: containers,
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(checkpointPath, podCheckpointManifestFile), data, 0o600))
	for _, container := range containers {
		if container.Archive == "" || filepath.Base(container.Archive) != container.Archive || !filepath.IsLocal(container.Archive) {
			continue
		}
		require.NoError(t, os.WriteFile(filepath.Join(checkpointPath, container.Archive), []byte("checkpoint"), 0o600))
	}
	return checkpointPath
}

func writeRestoreCheckpoint(t *testing.T, containerNames ...string) string {
	t.Helper()
	containers := make([]podCheckpointManifestContainer, 0, len(containerNames))
	for i, name := range containerNames {
		id := fmt.Sprintf("container-id-%d", i)
		containers = append(containers, podCheckpointManifestContainer{
			Name:    name,
			ID:      id,
			Archive: checkpointArchiveName(id),
		})
	}
	return writeRestoreCheckpointEntries(t, containers...)
}

func restoreRequest(checkpointPath string, containerNames ...string) *runtime.RestorePodRequest {
	configs := make([]*runtime.ContainerConfig, 0, len(containerNames))
	for _, name := range containerNames {
		configs = append(configs, &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{Name: name},
			Image:    &runtime.ImageSpec{Image: "original-image"},
			LogPath:  name + ".log",
		})
	}
	return &runtime.RestorePodRequest{
		CheckpointPath: checkpointPath,
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{Name: "pod", Namespace: "default", Uid: "uid"},
		},
		ContainerConfigs: configs,
	}
}

func TestPrepareRestoreRequest(t *testing.T) {
	checkpointPath := writeRestoreCheckpoint(t, "app")
	request := restoreRequest(checkpointPath, "app")

	containers, err := prepareRestoreRequest(request)
	require.NoError(t, err)
	require.Len(t, containers, 1)
	assert.Equal(t, "app", containers[0].checkpointName)
	assert.Equal(t, filepath.Join(checkpointPath, checkpointArchiveName("container-id-0")), containers[0].checkpointPath)
	assert.Equal(t, containers[0].checkpointPath, containers[0].config.GetImage().GetImage())
	assert.Equal(t, "original-image", request.ContainerConfigs[0].GetImage().GetImage(), "request config must not be mutated")
}

func TestPrepareRestoreRequestRejectsInvalidInput(t *testing.T) {
	validPath := writeRestoreCheckpoint(t, "app")
	regularFile := filepath.Join(t.TempDir(), "checkpoint")
	require.NoError(t, os.WriteFile(regularFile, nil, 0o600))
	symlinkPath := filepath.Join(t.TempDir(), "checkpoint-link")
	require.NoError(t, os.Symlink(validPath, symlinkPath))

	tests := []struct {
		name    string
		request func(t *testing.T) *runtime.RestorePodRequest
		wantErr string
	}{
		{
			name:    "nil request",
			request: func(*testing.T) *runtime.RestorePodRequest { return nil },
			wantErr: "restore request is required",
		},
		{
			name: "relative path",
			request: func(*testing.T) *runtime.RestorePodRequest {
				return restoreRequest("checkpoint", "app")
			},
			wantErr: "must be absolute",
		},
		{
			name: "missing path",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				return restoreRequest(filepath.Join(t.TempDir(), "missing"), "app")
			},
			wantErr: "existing directory",
		},
		{
			name: "regular file",
			request: func(*testing.T) *runtime.RestorePodRequest {
				return restoreRequest(regularFile, "app")
			},
			wantErr: "is not a directory",
		},
		{
			name: "symlink path",
			request: func(*testing.T) *runtime.RestorePodRequest {
				return restoreRequest(symlinkPath, "app")
			},
			wantErr: "must not be a symbolic link",
		},
		{
			name: "missing sandbox config",
			request: func(*testing.T) *runtime.RestorePodRequest {
				request := restoreRequest(validPath, "app")
				request.Config = nil
				return request
			},
			wantErr: "pod sandbox config is required",
		},
		{
			name: "missing checkpoint sandbox config",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				require.NoError(t, os.Remove(filepath.Join(path, podCheckpointConfigFile)))
				return restoreRequest(path, "app")
			},
			wantErr: "failed to inspect checkpoint sandbox config",
		},
		{
			name: "checkpoint sandbox config symlink",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				configPath := filepath.Join(path, podCheckpointConfigFile)
				require.NoError(t, os.Remove(configPath))
				require.NoError(t, os.Symlink(filepath.Join(path, "missing"), configPath))
				return restoreRequest(path, "app")
			},
			wantErr: "checkpoint sandbox config is not a regular file",
		},
		{
			name: "malformed checkpoint sandbox config",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				require.NoError(t, os.WriteFile(filepath.Join(path, podCheckpointConfigFile), []byte("{"), 0o600))
				return restoreRequest(path, "app")
			},
			wantErr: "failed to unmarshal checkpoint sandbox config",
		},
		{
			name: "incompatible checkpoint sandbox config",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				request := restoreRequest(path, "app")
				request.Config.Hostname = "different-hostname"
				return request
			},
			wantErr: "restore sandbox hostname",
		},
		{
			name: "malformed manifest",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(path, "checkpoint-manifest.json"), []byte("{"), 0o600))
				return restoreRequest(path, "app")
			},
			wantErr: "failed to unmarshal checkpoint manifest",
		},
		{
			name: "multiple manifest values",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				manifestPath := filepath.Join(path, podCheckpointManifestFile)
				data, err := os.ReadFile(manifestPath)
				require.NoError(t, err)
				data = append(data, []byte("\n{}")...)
				require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
				return restoreRequest(path, "app")
			},
			wantErr: "checkpoint manifest contains multiple JSON values",
		},
		{
			name: "opaque restore options in manifest",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				manifest := map[string]any{
					"version":   podCheckpointManifestVersion,
					"sandboxId": "sandbox-original",
					"containers": []podCheckpointManifestContainer{{
						Name: "app", ID: "container-id-0", Archive: checkpointArchiveName("container-id-0"),
					}},
					"restoreOptions": map[string]string{"example.runtime/lazy": "true"},
				}
				data, err := json.Marshal(manifest)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(path, podCheckpointManifestFile), data, 0o600))
				return restoreRequest(path, "app")
			},
			wantErr: `unknown field "restoreOptions"`,
		},
		{
			name: "unsupported manifest version",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				data, err := json.Marshal(podCheckpointManifest{
					Version:   podCheckpointManifestVersion + 1,
					SandboxID: "sandbox-original",
				})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(path, podCheckpointManifestFile), data, 0o600))
				return restoreRequest(path, "app")
			},
			wantErr: "manifest version",
		},
		{
			name: "empty manifest",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				return restoreRequest(writeRestoreCheckpoint(t), "app")
			},
			wantErr: "contains no containers",
		},
		{
			name: "missing manifest sandbox ID",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				manifest := podCheckpointManifest{
					Version: podCheckpointManifestVersion,
					Containers: []podCheckpointManifestContainer{{
						Name: "app", ID: "container-id-0", Archive: checkpointArchiveName("container-id-0"),
					}},
				}
				data, err := json.Marshal(manifest)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(path, podCheckpointManifestFile), data, 0o600))
				return restoreRequest(path, "app")
			},
			wantErr: "has no sandbox ID",
		},
		{
			name: "manifest symlink",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := t.TempDir()
				target := filepath.Join(path, "manifest-target")
				require.NoError(t, os.WriteFile(target, []byte("{}"), 0o600))
				require.NoError(t, os.Symlink(target, filepath.Join(path, podCheckpointManifestFile)))
				return restoreRequest(path, "app")
			},
			wantErr: "manifest is not a regular file",
		},
		{
			name: "missing archive",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				require.NoError(t, os.Remove(filepath.Join(path, checkpointArchiveName("container-id-0"))))
				return restoreRequest(path, "app")
			},
			wantErr: "is not accessible",
		},
		{
			name: "archive symlink",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app")
				archive := filepath.Join(path, checkpointArchiveName("container-id-0"))
				require.NoError(t, os.Remove(archive))
				require.NoError(t, os.Symlink(filepath.Join(path, "missing"), archive))
				return restoreRequest(path, "app")
			},
			wantErr: "is not a regular file",
		},
		{
			name: "invalid archive name",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpointEntries(t, podCheckpointManifestContainer{
					Name: "app", ID: "container-id", Archive: "../escape.tar",
				})
				return restoreRequest(path, "app")
			},
			wantErr: "invalid archive name",
		},
		{
			name: "duplicate manifest container ID",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				id := "container-id"
				path := writeRestoreCheckpointEntries(t,
					podCheckpointManifestContainer{Name: "app", ID: id, Archive: checkpointArchiveName(id)},
					podCheckpointManifestContainer{Name: "sidecar", ID: id, Archive: checkpointArchiveName(id)},
				)
				return restoreRequest(path, "app", "sidecar")
			},
			wantErr: "duplicate container ID",
		},
		{
			name: "missing container config",
			request: func(*testing.T) *runtime.RestorePodRequest {
				return restoreRequest(validPath, "other")
			},
			wantErr: "has no matching container config",
		},
		{
			name: "extra container config",
			request: func(*testing.T) *runtime.RestorePodRequest {
				return restoreRequest(validPath, "app", "completed-init")
			},
			wantErr: "has 2 container configs, but checkpoint manifest has 1 container entries",
		},
		{
			name: "duplicate container config",
			request: func(*testing.T) *runtime.RestorePodRequest {
				request := restoreRequest(validPath, "app", "app")
				return request
			},
			wantErr: "is duplicated",
		},
		{
			name: "missing container image",
			request: func(*testing.T) *runtime.RestorePodRequest {
				request := restoreRequest(validPath, "app")
				request.ContainerConfigs[0].Image = nil
				return request
			},
			wantErr: "has no image",
		},
		{
			name: "missing container log path",
			request: func(*testing.T) *runtime.RestorePodRequest {
				request := restoreRequest(validPath, "app")
				request.ContainerConfigs[0].LogPath = ""
				return request
			},
			wantErr: "has no log path",
		},
		{
			name: "duplicate manifest container",
			request: func(t *testing.T) *runtime.RestorePodRequest {
				path := writeRestoreCheckpoint(t, "app", "app")
				return restoreRequest(path, "app")
			},
			wantErr: "duplicate container name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := prepareRestoreRequest(test.request(t))
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

type fakePodRestoreOperations struct {
	runRequest      *runtime.RunPodSandboxRequest
	createRequests  []*runtime.CreateContainerRequest
	removeRequests  []*runtime.RemovePodSandboxRequest
	runError        error
	createError     error
	containerIDs    []string
	removeError     error
	onRun           func()
	cleanupCtxErr   error
	cleanupDeadline bool
	createContexts  []context.Context
}

func (f *fakePodRestoreOperations) RunPodSandbox(_ context.Context, request *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error) {
	f.runRequest = request
	if f.onRun != nil {
		f.onRun()
	}
	if f.runError != nil {
		return nil, f.runError
	}
	return &runtime.RunPodSandboxResponse{PodSandboxId: "sandbox-restored"}, nil
}

func (f *fakePodRestoreOperations) CreateContainer(ctx context.Context, request *runtime.CreateContainerRequest) (*runtime.CreateContainerResponse, error) {
	f.createContexts = append(f.createContexts, ctx)
	f.createRequests = append(f.createRequests, request)
	if f.createError != nil {
		return nil, f.createError
	}
	containerID := "container-restored"
	if len(f.containerIDs) >= len(f.createRequests) {
		containerID = f.containerIDs[len(f.createRequests)-1]
	}
	return &runtime.CreateContainerResponse{ContainerId: containerID}, nil
}

func (f *fakePodRestoreOperations) RemovePodSandbox(ctx context.Context, request *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	f.removeRequests = append(f.removeRequests, request)
	f.cleanupCtxErr = ctx.Err()
	_, f.cleanupDeadline = ctx.Deadline()
	if f.removeError != nil {
		return nil, f.removeError
	}
	return &runtime.RemovePodSandboxResponse{}, nil
}

func TestRestorePodOrchestration(t *testing.T) {
	request := &runtime.RestorePodRequest{
		CheckpointPath: "/checkpoint",
		Config:         &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "pod"}},
		RuntimeHandler: "restore-handler",
	}
	containers := []restoreContainer{
		{
			checkpointName: "app",
			checkpointPath: "/checkpoint/container-app.tar",
			config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{Name: "app"},
				Image:    &runtime.ImageSpec{Image: "/checkpoint/container-app.tar"},
			},
		},
		{
			checkpointName: "sidecar",
			checkpointPath: "/checkpoint/container-sidecar.tar",
			config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{Name: "sidecar"},
				Image:    &runtime.ImageSpec{Image: "/checkpoint/container-sidecar.tar"},
			},
		},
	}
	operations := &fakePodRestoreOperations{containerIDs: []string{"container-app", "container-sidecar"}}

	response, err := restorePod(context.Background(), request, containers, operations)
	require.NoError(t, err)
	assert.Equal(t, "sandbox-restored", response.GetPodSandboxId())
	require.NotNil(t, operations.runRequest)
	assert.Equal(t, "restore-handler", operations.runRequest.GetRuntimeHandler())
	require.Len(t, operations.createRequests, 2)
	require.Len(t, operations.createContexts, 2)
	assert.True(t, isPodRestoreContainerContext(operations.createContexts[0]))
	assert.True(t, isPodRestoreContainerContext(operations.createContexts[1]))
	assert.Equal(t, "sandbox-restored", operations.createRequests[0].GetPodSandboxId())
	assert.Equal(t, []*runtime.RestoredContainer{
		{Name: "app", ContainerId: "container-app"},
		{Name: "sidecar", ContainerId: "container-sidecar"},
	}, response.GetRestoredContainers())
	assert.Empty(t, operations.removeRequests)
}

func TestRestorePodRollsBackOnFailure(t *testing.T) {
	request := &runtime.RestorePodRequest{
		CheckpointPath: "/checkpoint",
		Config:         &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "pod"}},
	}
	containers := []restoreContainer{{
		checkpointName: "app",
		config:         &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "app"}},
	}}

	tests := []struct {
		name       string
		configure  func(context.CancelFunc, *fakePodRestoreOperations)
		wantErr    string
		wantRemove int
	}{
		{
			name: "run sandbox failure",
			configure: func(_ context.CancelFunc, operations *fakePodRestoreOperations) {
				operations.runError = errors.New("run failed")
			},
			wantErr: "failed to create sandbox for restore",
		},
		{
			name: "cancellation after sandbox creation",
			configure: func(cancel context.CancelFunc, operations *fakePodRestoreOperations) {
				operations.onRun = cancel
			},
			wantErr:    "pod restore aborted",
			wantRemove: 1,
		},
		{
			name: "create failure",
			configure: func(_ context.CancelFunc, operations *fakePodRestoreOperations) {
				operations.createError = errors.New("create failed")
			},
			wantErr:    "failed to create restored container",
			wantRemove: 1,
		},
		{
			name: "rollback failure is returned",
			configure: func(_ context.CancelFunc, operations *fakePodRestoreOperations) {
				operations.createError = errors.New("create failed")
				operations.removeError = errors.New("remove failed")
			},
			wantErr:    "failed to roll back restored sandbox",
			wantRemove: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			operations := &fakePodRestoreOperations{}
			test.configure(cancel, operations)

			_, err := restorePod(ctx, request, containers, operations)
			require.ErrorContains(t, err, test.wantErr)
			assert.Len(t, operations.removeRequests, test.wantRemove)
			if test.wantRemove != 0 {
				assert.NoError(t, operations.cleanupCtxErr)
				assert.True(t, operations.cleanupDeadline)
				assert.Equal(t, "sandbox-restored", operations.removeRequests[0].GetPodSandboxId())
			}
		})
	}
}

func TestRestorePodRollsBackDuplicateContainerID(t *testing.T) {
	request := &runtime.RestorePodRequest{
		CheckpointPath: "/checkpoint",
		Config:         &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "pod"}},
	}
	containers := []restoreContainer{
		{
			checkpointName: "app",
			config:         &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "app"}},
		},
		{
			checkpointName: "sidecar",
			config:         &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "sidecar"}},
		},
	}
	operations := &fakePodRestoreOperations{containerIDs: []string{"duplicate-id", "duplicate-id"}}

	_, err := restorePod(context.Background(), request, containers, operations)
	require.ErrorContains(t, err, "duplicate container ID")
	assert.Len(t, operations.createRequests, 2)
	require.Len(t, operations.removeRequests, 1)
	assert.Equal(t, "sandbox-restored", operations.removeRequests[0].GetPodSandboxId())
}
