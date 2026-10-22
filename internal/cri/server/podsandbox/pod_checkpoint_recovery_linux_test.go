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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	tasktypes "github.com/containerd/containerd/api/types/task"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/internal/cri/constants"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestBeginPodCheckpointRequiresThawedCgroup(t *testing.T) {
	for _, test := range []struct {
		name     string
		state    podCgroupState
		stateErr error
		canceled bool
		wantErr  string
	}{
		{name: "thawed", state: podCgroupStateThawed},
		{name: "frozen", state: podCgroupStateFrozen, wantErr: "cgroup is frozen"},
		{name: "transitioning", state: podCgroupStateTransitioning, wantErr: "cgroup is transitioning"},
		{name: "unknown", state: podCgroupStateUnknown, wantErr: "cgroup is unknown"},
		{name: "state lookup fails", stateErr: errors.New("state lookup failed"), wantErr: "state lookup failed"},
		{name: "canceled", state: podCgroupStateThawed, canceled: true, wantErr: "context canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &CheckpointService{rootDir: t.TempDir()}
			marker := podCheckpointRecoveryMarker{
				Version:      podCheckpointMarkerVersion,
				Namespace:    constants.K8sContainerdNamespace,
				SandboxID:    "sandbox-1",
				CgroupParent: "/kubepods/pod-1",
				ContainerIDs: []string{"container-a"},
			}
			var events []string
			freezer := &recordingPodCgroupFreezer{state: test.state, stateErr: test.stateErr, events: &events}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}

			active, err := service.beginPodCheckpoint(ctx, freezer, marker)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				assert.False(t, active)
				markers, err := service.loadPodCheckpointMarkers()
				require.NoError(t, err)
				assert.Empty(t, markers, "a rejected checkpoint must not authorize recovery")
			} else {
				require.NoError(t, err)
				assert.True(t, active)
				markers, err := service.loadPodCheckpointMarkers()
				require.NoError(t, err)
				assert.Equal(t, []podCheckpointRecoveryMarker{marker}, markers)
			}
			assert.Empty(t, events, "the marker must precede any freeze or pause request")
		})
	}
}

func TestPodCheckpointMarkerRoundTrip(t *testing.T) {
	c := &CheckpointService{rootDir: t.TempDir()}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		Namespace:    constants.K8sContainerdNamespace,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a", "container-b"},
	}

	active, err := c.writePodCheckpointMarker(marker)
	require.NoError(t, err)
	require.True(t, active)
	markerInfo, err := os.Lstat(c.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), markerInfo.Mode().Perm())
	dirInfo, err := os.Lstat(c.podCheckpointMarkerDirectory())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	markers, err := c.loadPodCheckpointMarkers()
	require.NoError(t, err)
	require.Equal(t, []podCheckpointRecoveryMarker{marker}, markers)
	active, err = c.writePodCheckpointMarker(marker)
	require.ErrorContains(t, err, "already exists")
	require.False(t, active)

	require.NoError(t, c.removePodCheckpointMarker(marker.Namespace, marker.SandboxID))
	markers, err = c.loadPodCheckpointMarkers()
	require.NoError(t, err)
	assert.Empty(t, markers)
}

func TestPodCheckpointRecoveryCleansImagesAfterResume(t *testing.T) {
	for _, test := range []struct {
		name            string
		failedContainer string
	}{
		{name: "export interrupted"},
		{name: "image cleanup fails", failedContainer: "container-a"},
		{name: "partial image cleanup", failedContainer: "container-b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			imageStore := &checkpointRecoveryImageStore{}
			if test.failedContainer != "" {
				imageStore.deleteErr = errdefs.ErrUnavailable
				imageStore.deleteErrName = checkpointImageName(test.failedContainer)
			}
			client, err := containerd.New("", containerd.WithServices(
				containerd.WithImageStore(imageStore),
			))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, client.Close()) })
			service := &CheckpointService{client: client, rootDir: t.TempDir()}
			marker := podCheckpointRecoveryMarker{
				Version:      podCheckpointMarkerVersion,
				Namespace:    constants.K8sContainerdNamespace,
				SandboxID:    "sandbox-1",
				CgroupParent: "/kubepods/pod-1",
				ContainerIDs: []string{"container-a", "container-b"},
			}
			active, err := service.writePodCheckpointMarker(marker)
			require.NoError(t, err)
			require.True(t, active)
			require.NoError(t, service.markPodCheckpointResumed(marker))

			// A crash while updating the phase may leave a temporary file.
			temp := filepath.Join(service.podCheckpointMarkerDirectory(), ".marker-tmp-interrupted")
			require.NoError(t, os.WriteFile(temp, []byte("partial marker"), 0o600))
			markers, err := service.loadPodCheckpointMarkers()
			require.NoError(t, err)
			marker.TasksResumed = true
			require.Equal(t, []podCheckpointRecoveryMarker{marker}, markers)
			_, err = os.Stat(temp)
			require.ErrorIs(t, err, os.ErrNotExist)

			// Simulate a daemon restart during export. No task services are
			// configured: cleanup must not inspect or resume the containers.
			restarted := &CheckpointService{client: client, rootDir: service.rootDir}
			err = restarted.Recover(t.Context())
			var previouslyDeleted []string
			if test.failedContainer != "" {
				require.ErrorIs(t, err, errdefs.ErrUnavailable)
				if test.failedContainer == "container-b" {
					previouslyDeleted = []string{checkpointImageName("container-a")}
				}
				assert.Equal(t, previouslyDeleted, imageStore.deleted)
				markers, err := restarted.loadPodCheckpointMarkers()
				require.NoError(t, err)
				require.Equal(t, []podCheckpointRecoveryMarker{marker}, markers)

				imageStore.deleteErr = nil
				require.NoError(t, restarted.Recover(t.Context()))
			} else {
				require.NoError(t, err)
			}
			wantDeleted := append(previouslyDeleted, checkpointImageName("container-a"), checkpointImageName("container-b"))
			assert.Equal(t, wantDeleted, imageStore.deleted)
			markers, err = restarted.loadPodCheckpointMarkers()
			require.NoError(t, err)
			assert.Empty(t, markers, "remove the marker only after all images are deleted")
		})
	}
}

func TestPodCheckpointResumedMarkerPublicationFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		syncFail bool
	}{
		{name: "rename fails"},
		{name: "directory sync fails", syncFail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &CheckpointService{rootDir: t.TempDir()}
			marker := podCheckpointRecoveryMarker{
				Version:      podCheckpointMarkerVersion,
				Namespace:    constants.K8sContainerdNamespace,
				SandboxID:    "sandbox-1",
				CgroupParent: "/kubepods/pod-1",
				ContainerIDs: []string{"container-a"},
			}
			_, err := service.writePodCheckpointMarker(marker)
			require.NoError(t, err)
			publishErr := errors.New("injected resumed marker failure")
			operations := defaultPodCheckpointMarkerFileOperations
			if test.syncFail {
				operations.syncDirectory = func(string) error { return publishErr }
			} else {
				operations.rename = func(string, string) error { return publishErr }
			}

			require.ErrorIs(t, service.markPodCheckpointResumedWithFileOperations(marker, operations), publishErr)
			markers, err := service.loadPodCheckpointMarkers()
			require.NoError(t, err)
			marker.TasksResumed = test.syncFail
			require.Equal(t, []podCheckpointRecoveryMarker{marker}, markers, "publication failure must retain recovery ownership")
			entries, err := os.ReadDir(service.podCheckpointMarkerDirectory())
			require.NoError(t, err)
			require.Len(t, entries, 1, "failed publication must remove temporary marker files")

			require.NoError(t, service.markPodCheckpointResumed(marker))
			markers, err = service.loadPodCheckpointMarkers()
			require.NoError(t, err)
			marker.TasksResumed = true
			require.Equal(t, []podCheckpointRecoveryMarker{marker}, markers)
		})
	}
}

func TestPodCheckpointMarkerPostPublishFailureRollsBack(t *testing.T) {
	c := &CheckpointService{rootDir: t.TempDir()}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		Namespace:    constants.K8sContainerdNamespace,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a"},
	}
	syncCalls := 0
	operations := defaultPodCheckpointMarkerFileOperations
	operations.syncDirectory = func(path string) error {
		syncCalls++
		if syncCalls == 1 {
			return errors.New("injected publish sync failure")
		}
		return syncDirectory(path)
	}

	active, err := c.writePodCheckpointMarkerWithFileOperations(marker, operations)
	require.ErrorContains(t, err, "injected publish sync failure")
	assert.False(t, active)
	_, statErr := os.Lstat(c.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestPodCheckpointMarkerPostPublishRollbackFailureStaysActive(t *testing.T) {
	c := &CheckpointService{rootDir: t.TempDir()}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		Namespace:    constants.K8sContainerdNamespace,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a"},
	}
	operations := defaultPodCheckpointMarkerFileOperations
	operations.syncDirectory = func(string) error { return errors.New("injected publish sync failure") }
	operations.remove = func(path string) error {
		if path == c.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID) {
			return errors.New("injected rollback failure")
		}
		return os.Remove(path)
	}

	active, err := c.writePodCheckpointMarkerWithFileOperations(marker, operations)
	require.ErrorContains(t, err, "injected publish sync failure")
	require.ErrorContains(t, err, "injected rollback failure")
	assert.True(t, active)
	_, statErr := os.Lstat(c.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
	require.NoError(t, statErr)
}

func TestValidatePodCheckpointMarker(t *testing.T) {
	valid := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		Namespace:    constants.K8sContainerdNamespace,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a"},
	}
	for _, test := range []struct {
		name    string
		mutate  func(*podCheckpointRecoveryMarker)
		wantErr string
	}{
		{name: "valid"},
		{name: "with output", mutate: func(marker *podCheckpointRecoveryMarker) { marker.OutputPath = "/checkpoint/out" }},
		{name: "old version", mutate: func(marker *podCheckpointRecoveryMarker) { marker.Version = 1 }, wantErr: "version"},
		{name: "version", mutate: func(marker *podCheckpointRecoveryMarker) { marker.Version++ }, wantErr: "version"},
		{name: "no namespace", mutate: func(marker *podCheckpointRecoveryMarker) { marker.Namespace = "" }, wantErr: "invalid namespace"},
		{name: "relative output", mutate: func(marker *podCheckpointRecoveryMarker) { marker.OutputPath = "out" }, wantErr: "invalid output path"},
		{name: "unclean output", mutate: func(marker *podCheckpointRecoveryMarker) { marker.OutputPath = "/out/../x" }, wantErr: "invalid output path"},
		{name: "sandbox", mutate: func(marker *podCheckpointRecoveryMarker) { marker.SandboxID = "" }, wantErr: "no sandbox ID"},
		{name: "cgroup", mutate: func(marker *podCheckpointRecoveryMarker) { marker.CgroupParent = "/" }, wantErr: "invalid cgroup parent"},
		{name: "containers", mutate: func(marker *podCheckpointRecoveryMarker) { marker.ContainerIDs = nil }, wantErr: "no container IDs"},
		{name: "duplicate", mutate: func(marker *podCheckpointRecoveryMarker) {
			marker.ContainerIDs = []string{"container-a", "container-a"}
		}, wantErr: "duplicate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker := valid
			marker.ContainerIDs = append([]string(nil), valid.ContainerIDs...)
			if test.mutate != nil {
				test.mutate(&marker)
			}
			err := validatePodCheckpointMarker(marker)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestPodCheckpointRecoveryUsesCRINamespace(t *testing.T) {
	if cgroups.Mode() != cgroups.Unified {
		t.Skip("requires cgroup v2 to simulate a missing Pod cgroup")
	}
	ctx := t.Context()
	root := t.TempDir()
	blobs, err := local.NewStore(filepath.Join(root, "content"))
	require.NoError(t, err)
	bdb, err := bolt.Open(filepath.Join(root, "metadata.db"), 0o600, nil)
	require.NoError(t, err)
	db := metadata.NewDB(bdb, blobs, nil)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	require.NoError(t, db.Init(ctx))
	imageStore := metadata.NewImageStore(db)
	client, err := containerd.New("",
		containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
		containerd.WithServices(
			containerd.WithContainerStore(metadata.NewContainerStore(db)),
			containerd.WithImageStore(imageStore),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	service := &CheckpointService{client: client, rootDir: root}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		Namespace:    constants.K8sContainerdNamespace,
		SandboxID:    "sandbox-1",
		CgroupParent: filepath.Join("/", filepath.Base(filepath.Dir(root)), filepath.Base(root)),
		ContainerIDs: []string{"container-a"},
	}
	// Simulate recovery after a reboot without creating or changing host cgroups.
	_, err = os.Stat(filepath.Join("/sys/fs/cgroup", marker.CgroupParent))
	require.ErrorIs(t, err, os.ErrNotExist)
	active, err := service.writePodCheckpointMarker(marker)
	require.NoError(t, err)
	require.True(t, active)
	imageName := checkpointImageName(marker.ContainerIDs[0])
	for _, namespace := range []string{constants.K8sContainerdNamespace, "other"} {
		_, err = imageStore.Create(namespaces.WithNamespace(ctx, namespace), images.Image{
			Name: imageName,
			Target: imagespec.Descriptor{
				MediaType: imagespec.MediaTypeImageIndex,
				Digest:    digest.FromString("checkpoint"),
				Size:      1,
			},
		})
		require.NoError(t, err)
	}

	// Plugin initialization supplies a context without a namespace.
	require.NoError(t, service.Recover(ctx))
	_, err = os.Stat(service.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = imageStore.Get(namespaces.WithNamespace(ctx, constants.K8sContainerdNamespace), imageName)
	require.True(t, errdefs.IsNotFound(err), "Kubernetes checkpoint image must be removed: %v", err)
	_, err = imageStore.Get(namespaces.WithNamespace(ctx, "other"), imageName)
	require.NoError(t, err, "recovery must preserve images in other namespaces")
}

func TestRecoverMissingCheckpointCgroup(t *testing.T) {
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		Namespace:    constants.K8sContainerdNamespace,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a", "container-b"},
	}
	for _, test := range []struct {
		name              string
		deletedContainers bool
		existingTaskID    string
		containerErrorID  string
		taskErrorID       string
		wantErr           string
	}{
		{name: "metadata survives reboot"},
		{name: "containers already deleted", deletedContainers: true},
		{name: "sandbox task remains", existingTaskID: marker.SandboxID, wantErr: marker.SandboxID},
		{name: "workload task remains", existingTaskID: "container-b", wantErr: "container-b"},
		{name: "sandbox metadata lookup fails", containerErrorID: marker.SandboxID, wantErr: marker.SandboxID},
		{name: "workload metadata lookup fails", containerErrorID: "container-b", wantErr: "container-b"},
		{name: "sandbox task lookup fails", taskErrorID: marker.SandboxID, wantErr: marker.SandboxID},
		{name: "workload task lookup fails", taskErrorID: "container-b", wantErr: "container-b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			containerStore := &checkpointRecoveryContainerStore{
				ids:     append([]string{marker.SandboxID}, marker.ContainerIDs...),
				errorID: test.containerErrorID,
			}
			if test.deletedContainers {
				containerStore.ids = nil
			}
			imageStore := &checkpointRecoveryImageStore{}
			client, err := containerd.New("", containerd.WithServices(
				containerd.WithContainerStore(containerStore),
				containerd.WithTaskClient(&checkpointRecoveryTaskClient{
					existingID: test.existingTaskID,
					errorID:    test.taskErrorID,
				}),
				containerd.WithImageStore(imageStore),
			))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, client.Close()) })
			service := &CheckpointService{client: client, rootDir: t.TempDir()}
			active, err := service.writePodCheckpointMarker(marker)
			require.NoError(t, err)
			require.True(t, active)

			err = service.recoverMissingCheckpointCgroup(t.Context(), marker)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				if test.containerErrorID != "" || test.taskErrorID != "" {
					assert.ErrorIs(t, err, errdefs.ErrUnavailable)
				}
				assert.Empty(t, imageStore.deleted, "uncertain recovery must retain checkpoint images")
				_, err = os.Stat(service.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
				require.NoError(t, err, "uncertain recovery must retain the marker")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []string{checkpointImageName("container-a"), checkpointImageName("container-b")}, imageStore.deleted)
			_, err = os.Stat(service.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
			require.ErrorIs(t, err, os.ErrNotExist)
			if !test.deletedContainers {
				for _, id := range containerStore.ids {
					_, err := client.LoadContainer(t.Context(), id)
					require.NoError(t, err, "recovery must leave persistent container metadata for CRI to reload")
				}
			}
		})
	}
}

type checkpointRecoveryContainerStore struct {
	containers.Store
	ids     []string
	errorID string
}

func (s *checkpointRecoveryContainerStore) Get(_ context.Context, id string) (containers.Container, error) {
	if id == s.errorID {
		return containers.Container{}, errdefs.ErrUnavailable
	}
	for _, existingID := range s.ids {
		if id == existingID {
			return containers.Container{ID: id}, nil
		}
	}
	return containers.Container{}, errdefs.ErrNotFound
}

type checkpointRecoveryTaskClient struct {
	tasks.TasksClient
	existingID string
	errorID    string
}

func (c *checkpointRecoveryTaskClient) Get(_ context.Context, request *tasks.GetRequest, _ ...grpc.CallOption) (*tasks.GetResponse, error) {
	if request.ContainerID == c.errorID {
		return nil, status.Error(codes.Unavailable, "task lookup failed")
	}
	if request.ContainerID == c.existingID {
		return &tasks.GetResponse{Process: &tasktypes.Process{
			ID: request.ContainerID, Pid: 123, Status: tasktypes.Status_PAUSED,
		}}, nil
	}
	return nil, status.Error(codes.NotFound, "task no longer exists")
}

type checkpointRecoveryImageStore struct {
	images.Store
	mu                sync.Mutex
	deleted           []string
	deletedNamespaces []string
	deleteErr         error
	deleteErrName     string
}

func (s *checkpointRecoveryImageStore) Delete(ctx context.Context, name string, _ ...images.DeleteOpt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil && (s.deleteErrName == "" || s.deleteErrName == name) {
		return s.deleteErr
	}
	namespace, _ := namespaces.Namespace(ctx)
	s.deleted = append(s.deleted, name)
	s.deletedNamespaces = append(s.deletedNamespaces, namespace)
	return nil
}

func (s *checkpointRecoveryImageStore) setDeleteErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteErr = err
}

func newResumedMarkerTestService(t *testing.T) (*CheckpointService, *checkpointRecoveryImageStore) {
	t.Helper()
	imageStore := &checkpointRecoveryImageStore{}
	client, err := containerd.New("", containerd.WithServices(containerd.WithImageStore(imageStore)))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	return &CheckpointService{client: client, rootDir: t.TempDir()}, imageStore
}

func writeResumedTestMarker(t *testing.T, service *CheckpointService, marker podCheckpointRecoveryMarker) {
	t.Helper()
	active, err := service.writePodCheckpointMarker(marker)
	require.NoError(t, err)
	require.True(t, active)
	require.NoError(t, service.markPodCheckpointResumed(marker))
}

func testRecoveryMarker(namespace, sandboxID string, containerIDs ...string) podCheckpointRecoveryMarker {
	return podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		Namespace:    namespace,
		SandboxID:    sandboxID,
		CgroupParent: "/kubepods/" + sandboxID,
		ContainerIDs: containerIDs,
	}
}

func TestPodCheckpointRecoveryContinuesAfterFailure(t *testing.T) {
	service, imageStore := newResumedMarkerTestService(t)
	failing := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-a")
	healthy := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-2", "container-b")
	writeResumedTestMarker(t, service, failing)
	writeResumedTestMarker(t, service, healthy)
	imageStore.deleteErr = errdefs.ErrUnavailable
	imageStore.deleteErrName = checkpointImageName("container-a")
	// An unexpected entry is reported without blocking recovery of markers.
	stray := filepath.Join(service.podCheckpointMarkerDirectory(), "stray")
	require.NoError(t, os.WriteFile(stray, nil, 0o600))

	err := service.Recover(t.Context())
	require.ErrorIs(t, err, errdefs.ErrUnavailable)
	require.ErrorContains(t, err, "unexpected entry")
	assert.Equal(t, []string{checkpointImageName("container-b")}, imageStore.deleted)
	markers, err := service.loadPodCheckpointMarkers()
	require.Error(t, err)
	failing.TasksResumed = true
	assert.Equal(t, []podCheckpointRecoveryMarker{failing}, markers)
}

func TestPodCheckpointRecoverySkipsCheckpointInProgress(t *testing.T) {
	service, imageStore := newResumedMarkerTestService(t)
	marker := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-a")
	writeResumedTestMarker(t, service, marker)
	service.podCheckpointsInProgress.Store(podCheckpointKey(marker.Namespace, marker.SandboxID), struct{}{})

	require.NoError(t, service.Recover(t.Context()))
	assert.Empty(t, imageStore.deleted, "an active checkpoint owns its marker")
	_, err := os.Stat(service.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
	require.NoError(t, err)
}

func TestPodCheckpointRecoveryReloadsMarkerAfterReservation(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("removed=%t", remove), func(t *testing.T) {
			service, imageStore := newResumedMarkerTestService(t)
			first := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-a")
			second := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-2", "container-b")
			if podCheckpointMarkerFilename(first.Namespace, first.SandboxID) > podCheckpointMarkerFilename(second.Namespace, second.SandboxID) {
				first, second = second, first
			}
			second.CgroupParent = "/" + filepath.Base(t.TempDir())
			_, err := os.Stat(filepath.Join("/sys/fs/cgroup", second.CgroupParent))
			require.ErrorIs(t, err, os.ErrNotExist)
			writeResumedTestMarker(t, service, first)
			active, err := service.writePodCheckpointMarker(second)
			require.NoError(t, err)
			require.True(t, active)

			// The first image deletion runs after Recover has loaded both
			// markers. Complete the second checkpoint before its reservation
			// is acquired, as its active owner could do concurrently.
			store := &checkpointRecoveryMarkerChangeImageStore{
				checkpointRecoveryImageStore: imageStore,
				change: func() {
					if remove {
						require.NoError(t, service.removePodCheckpointMarker(second.Namespace, second.SandboxID))
					} else {
						require.NoError(t, service.markPodCheckpointResumed(second))
					}
				},
			}
			c, err := containerd.New("", containerd.WithServices(
				containerd.WithImageStore(store),
				containerd.WithContainerStore(&checkpointRecoveryContainerStore{errorID: second.ContainerIDs[0]}),
			))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, c.Close()) })
			service.client = c

			require.NoError(t, service.Recover(t.Context()))
			wantDeleted := []string{checkpointImageName(first.ContainerIDs[0])}
			if !remove {
				wantDeleted = append(wantDeleted, checkpointImageName(second.ContainerIDs[0]))
			}
			assert.Equal(t, wantDeleted, imageStore.deleted)
			markers, err := service.loadPodCheckpointMarkers()
			require.NoError(t, err)
			assert.Empty(t, markers)
		})
	}
}

type checkpointRecoveryMarkerChangeImageStore struct {
	*checkpointRecoveryImageStore
	change func()
}

func (s *checkpointRecoveryMarkerChangeImageStore) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	if s.change != nil {
		change := s.change
		s.change = nil
		change()
	}
	return s.checkpointRecoveryImageStore.Delete(ctx, name, opts...)
}

func TestPodCheckpointRecoveryResumesBeforeOutputCleanup(t *testing.T) {
	if cgroups.Mode() != cgroups.Unified {
		t.Skip("requires cgroup v2 to simulate a missing Pod cgroup")
	}
	service, imageStore := newResumedMarkerTestService(t)
	containerStore := &checkpointRecoveryContainerStore{}
	c, err := containerd.New("", containerd.WithServices(
		containerd.WithImageStore(imageStore),
		containerd.WithContainerStore(containerStore),
	))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, c.Close()) })
	service.client = c
	marker := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-a")
	marker.CgroupParent = "/" + filepath.Base(t.TempDir())
	_, err = os.Stat(filepath.Join("/sys/fs/cgroup", marker.CgroupParent))
	require.ErrorIs(t, err, os.ErrNotExist)
	marker.OutputPath = filepath.Join(t.TempDir(), "output")
	// A damaged output path must not prevent task recovery or discard the
	// resumed phase. Simulate absent tasks after a reboot without host writes.
	require.NoError(t, os.WriteFile(marker.OutputPath, nil, 0o600))
	active, err := service.writePodCheckpointMarker(marker)
	require.NoError(t, err)
	require.True(t, active)

	require.Error(t, service.Recover(t.Context()))
	stored, err := readPodCheckpointMarker(service.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
	require.NoError(t, err)
	assert.True(t, stored.TasksResumed, "record task recovery before attempting output cleanup")
	assert.Empty(t, imageStore.deleted, "retain cleanup ownership while output cleanup fails")

	require.NoError(t, os.Remove(marker.OutputPath))
	require.NoError(t, os.Mkdir(marker.OutputPath, 0o700))
	containerStore.errorID = marker.ContainerIDs[0]
	require.NoError(t, service.Recover(t.Context()), "cleanup retries must not inspect tasks again")
	assert.Equal(t, []string{checkpointImageName(marker.ContainerIDs[0])}, imageStore.deleted)
	_, err = os.Stat(service.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPodCheckpointRecoveryUsesMarkerNamespace(t *testing.T) {
	service, imageStore := newResumedMarkerTestService(t)
	writeResumedTestMarker(t, service, testRecoveryMarker("other", "sandbox-1", "container-a"))
	// The same sandbox ID in another namespace is a different checkpoint.
	writeResumedTestMarker(t, service, testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-b"))

	require.NoError(t, service.Recover(t.Context()))
	deleted := map[string]string{}
	for i, name := range imageStore.deleted {
		deleted[name] = imageStore.deletedNamespaces[i]
	}
	assert.Equal(t, map[string]string{
		checkpointImageName("container-a"): "other",
		checkpointImageName("container-b"): constants.K8sContainerdNamespace,
	}, deleted)
}

func TestBeginPodCheckpointRecoversStaleMarker(t *testing.T) {
	service, imageStore := newResumedMarkerTestService(t)
	stale := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-a")
	writeResumedTestMarker(t, service, stale)
	imageStore.deleteErr = errdefs.ErrUnavailable
	var events []string
	freezer := &recordingPodCgroupFreezer{state: podCgroupStateThawed, events: &events}

	marker := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-a", "container-b")
	active, err := service.beginPodCheckpoint(t.Context(), freezer, marker)
	require.ErrorIs(t, err, errdefs.ErrUnavailable)
	assert.False(t, active)

	imageStore.deleteErr = nil
	active, err = service.beginPodCheckpoint(t.Context(), freezer, marker)
	require.NoError(t, err)
	assert.True(t, active)
	assert.Equal(t, []string{checkpointImageName("container-a")}, imageStore.deleted)
	markers, err := service.loadPodCheckpointMarkers()
	require.NoError(t, err)
	assert.Equal(t, []podCheckpointRecoveryMarker{marker}, markers)
}

func TestRecoverInBackgroundRetries(t *testing.T) {
	initial, maximum := podCheckpointRecoveryRetryInitial, podCheckpointRecoveryRetryMax
	podCheckpointRecoveryRetryInitial, podCheckpointRecoveryRetryMax = time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { podCheckpointRecoveryRetryInitial, podCheckpointRecoveryRetryMax = initial, maximum })

	service, imageStore := newResumedMarkerTestService(t)
	marker := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-a")
	writeResumedTestMarker(t, service, marker)
	imageStore.setDeleteErr(errdefs.ErrUnavailable)
	service.RecoverInBackground()
	time.Sleep(20 * time.Millisecond)
	_, err := os.Stat(service.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
	require.NoError(t, err, "a failing recovery keeps its marker")

	imageStore.setDeleteErr(nil)
	require.Eventually(t, func() bool {
		_, err := os.Stat(service.podCheckpointMarkerPath(marker.Namespace, marker.SandboxID))
		return errors.Is(err, os.ErrNotExist)
	}, 5*time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		service.recoveryMu.Lock()
		defer service.recoveryMu.Unlock()
		return !service.recoveryRunning
	}, 5*time.Second, time.Millisecond)
}

func TestCleanupInterruptedCheckpointOutput(t *testing.T) {
	for _, test := range []struct {
		name     string
		manifest bool
		reserved bool
		wantKept []string
	}{
		{name: "partial output", wantKept: []string{"user-file"}},
		{name: "complete checkpoint", manifest: true, wantKept: []string{
			checkpointArchiveName("container-a"), checkpointArchiveTempPrefix + "1", podCheckpointManifestFile, podCheckpointConfigFile, "user-file",
		}},
		{name: "output in use", reserved: true, wantKept: []string{
			checkpointArchiveName("container-a"), checkpointArchiveTempPrefix + "1", podCheckpointConfigFile, "user-file",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &CheckpointService{rootDir: t.TempDir()}
			output, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			files := []string{checkpointArchiveName("container-a"), checkpointArchiveTempPrefix + "1", podCheckpointConfigFile, "user-file"}
			if test.manifest {
				files = append(files, podCheckpointManifestFile)
			}
			for _, name := range files {
				require.NoError(t, os.WriteFile(filepath.Join(output, name), nil, 0o600))
			}
			if test.reserved {
				service.podCheckpointOutputsInProgress.Store(output, struct{}{})
			}
			marker := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox-1", "container-a")
			marker.OutputPath = output

			require.NoError(t, service.cleanupInterruptedCheckpointOutput(marker))
			entries, err := os.ReadDir(output)
			require.NoError(t, err)
			var kept []string
			for _, entry := range entries {
				kept = append(kept, entry.Name())
			}
			sort.Strings(kept)
			sort.Strings(test.wantKept)
			assert.Equal(t, test.wantKept, kept)
		})
	}
}

// A task can exit while the daemon is down, or immediately after being resumed.
func TestPodCheckpointRecoveryHandlesExitedTasks(t *testing.T) {
	for _, test := range []struct {
		name        string
		statuses    []containerd.ProcessStatus
		statusErr   error
		wantErr     bool
		wantResumes int
	}{
		{name: "already stopped", statuses: []containerd.ProcessStatus{containerd.Stopped}},
		{name: "exits after resume", statuses: []containerd.ProcessStatus{containerd.Paused, containerd.Stopped}, wantResumes: 1},
		{name: "deleted task", statusErr: errdefs.ErrNotFound},
		{name: "lookup failure", statusErr: errdefs.ErrUnavailable, wantErr: true},
		{name: "unknown state", statuses: []containerd.ProcessStatus{containerd.Unknown}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := &exitingCheckpointTask{statuses: test.statuses, statusErr: test.statusErr}
			var events []string
			surviving := &recordingPodCheckpointTask{id: "surviving", status: containerd.Paused, events: &events}
			recovery := podCheckpointTaskRecovery{tasks: []podCheckpointRuntimeTask{task, surviving}}
			err := recovery.recover(t.Context())
			if test.wantErr {
				require.Error(t, err)
				require.Len(t, recovery.tasks, 1, "retain only the task whose recovery is uncertain")
			} else {
				require.NoError(t, err)
				require.Empty(t, recovery.tasks)
			}
			require.Equal(t, test.wantResumes, task.resumes)
			require.Equal(t, []string{"resume:surviving"}, events)
		})
	}
}

type exitingCheckpointTask struct {
	podCheckpointRuntimeTask
	statuses  []containerd.ProcessStatus
	statusErr error
	resumes   int
}

func (*exitingCheckpointTask) ID() string { return "exiting" }

func (t *exitingCheckpointTask) Status(context.Context) (containerd.Status, error) {
	if t.statusErr != nil {
		return containerd.Status{}, t.statusErr
	}
	state := t.statuses[0]
	if len(t.statuses) > 1 {
		t.statuses = t.statuses[1:]
	}
	return containerd.Status{Status: state}, nil
}

func (t *exitingCheckpointTask) Resume(context.Context) error {
	t.resumes++
	return nil
}

func TestPodCheckpointRecoveryPreservesActiveMarkerTemp(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		t.Run(fmt.Sprintf("resumed=%t", resumed), func(t *testing.T) {
			service := &CheckpointService{rootDir: t.TempDir()}
			marker := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox", "container")
			operations := defaultPodCheckpointMarkerFileOperations
			publish := func(source, destination string) error {
				_, err := service.loadPodCheckpointMarkers()
				require.NoError(t, err)
				_, err = os.Stat(source)
				require.NoError(t, err, "recovery must leave an active writer's temporary file")
				if resumed {
					return os.Rename(source, destination)
				}
				return os.Link(source, destination)
			}
			if resumed {
				_, err := service.writePodCheckpointMarker(marker)
				require.NoError(t, err)
				operations.rename = publish
				require.NoError(t, service.markPodCheckpointResumedWithFileOperations(marker, operations))
			} else {
				operations.link = publish
				active, err := service.writePodCheckpointMarkerWithFileOperations(marker, operations)
				require.NoError(t, err)
				require.True(t, active)
			}
			assert.Empty(t, service.markerTemps, "publication must release the temporary name")
		})
	}
}

func TestPodCheckpointRecoveryRetainsCompletedRuntimeWork(t *testing.T) {
	for _, failure := range []string{"rename", "directory sync", "partial resume"} {
		t.Run(failure, func(t *testing.T) {
			service, _ := newResumedMarkerTestService(t)
			marker := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox", "container-a", "container-b")
			_, err := service.writePodCheckpointMarker(marker)
			require.NoError(t, err)
			var events []string
			freezer := &recordingPodCgroupFreezer{state: podCgroupStateFrozen, events: &events}
			first := &recordingPodCheckpointTask{id: "container-a", status: containerd.Paused, events: &events}
			second := &recordingPodCheckpointTask{id: "container-b", status: containerd.Paused, events: &events}
			recovery := &podCheckpointTaskRecovery{freezer: freezer, tasks: []podCheckpointRuntimeTask{first, second}}
			key := podCheckpointKey(marker.Namespace, marker.SandboxID)
			service.podCheckpointRecoveries.Store(key, recovery)
			recoveryErr := errors.New("injected recovery failure")
			if failure == "partial resume" {
				second.resumeErr = recoveryErr
				require.ErrorIs(t, service.recoverPodCheckpointTasks(t.Context(), recovery), recoveryErr)
				second.resumeErr = nil
			} else {
				require.NoError(t, service.recoverPodCheckpointTasks(t.Context(), recovery))
				operations := defaultPodCheckpointMarkerFileOperations
				if failure == "rename" {
					operations.rename = func(string, string) error { return recoveryErr }
				} else {
					operations.syncDirectory = func(string) error { return recoveryErr }
				}
				require.ErrorIs(t, service.markPodCheckpointResumedWithFileOperations(marker, operations), recoveryErr)
			}
			// A completed step must not be replayed after another owner pauses
			// that task or freezes the Pod while metadata recovery is retried.
			first.status = containerd.Paused
			freezer.state = podCgroupStateFrozen
			previous := append([]string(nil), events...)
			require.NoError(t, service.Recover(t.Context()))
			if failure == "partial resume" {
				previous = append(previous, "resume:container-b")
			}
			assert.Equal(t, previous, events)
			assert.Equal(t, containerd.Paused, first.status)
			assert.Equal(t, podCgroupStateFrozen, freezer.state)
			_, cached := service.podCheckpointRecoveries.Load(key)
			assert.False(t, cached, "completed cleanup must release cached recovery state")
		})
	}
}

func TestPodCheckpointRecoveryRetainsThawAfterTaskLookupFailure(t *testing.T) {
	store := &checkpointRecoveryContainerStore{errorID: "container"}
	imageStore := &checkpointRecoveryImageStore{}
	client, err := containerd.New("", containerd.WithServices(
		containerd.WithContainerStore(store),
		containerd.WithImageStore(imageStore),
	))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	service := &CheckpointService{client: client, rootDir: t.TempDir()}
	marker := testRecoveryMarker(constants.K8sContainerdNamespace, "sandbox", "container")
	_, err = service.writePodCheckpointMarker(marker)
	require.NoError(t, err)
	var events []string
	freezer := &recordingPodCgroupFreezer{state: podCgroupStateFrozen, events: &events}
	recovery := &podCheckpointTaskRecovery{freezer: freezer, unloaded: []string{"container"}}
	service.podCheckpointRecoveries.Store(podCheckpointKey(marker.Namespace, marker.SandboxID), recovery)

	require.ErrorIs(t, service.Recover(t.Context()), errdefs.ErrUnavailable)
	assert.Equal(t, []string{"thaw"}, events, "failed task lookup must still thaw the Pod")
	store.errorID = ""
	freezer.state = podCgroupStateFrozen
	require.NoError(t, service.Recover(t.Context()))
	assert.Equal(t, []string{"thaw"}, events, "retry must not thaw another owner's freeze")
	assert.Equal(t, podCgroupStateFrozen, freezer.state)
}

func TestPodCheckpointRecoveryThawsBeforeTaskLookupDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var events []string
	freezer := &recordingPodCgroupFreezer{state: podCgroupStateFrozen, events: &events}
	store := &checkpointRecoveryDeadlineContainerStore{get: func(ctx context.Context, _ string) (containers.Container, error) {
		assert.Equal(t, []string{"thaw"}, events, "thaw must precede potentially blocking task lookups")
		cancel()
		<-ctx.Done()
		return containers.Container{}, ctx.Err()
	}}
	client, err := containerd.New("", containerd.WithServices(containerd.WithContainerStore(store)))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	service := &CheckpointService{client: client}
	recovery := &podCheckpointTaskRecovery{freezer: freezer, unloaded: []string{"container"}}
	err = service.recoverPodCheckpointTasks(namespaces.WithNamespace(ctx, constants.K8sContainerdNamespace), recovery)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, recovery.freezer, "retain a successful thaw after the lookup cancels the context")
	assert.Equal(t, []string{"container"}, recovery.unloaded)
}

type checkpointRecoveryDeadlineContainerStore struct {
	containers.Store
	get func(context.Context, string) (containers.Container, error)
}

func (s *checkpointRecoveryDeadlineContainerStore) Get(ctx context.Context, id string) (containers.Container, error) {
	return s.get(ctx, id)
}
