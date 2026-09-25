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
	"os"
	"path/filepath"
	"testing"

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

func TestPodCheckpointMarkerRoundTrip(t *testing.T) {
	c := &CheckpointService{rootDir: t.TempDir()}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a", "container-b"},
	}

	active, err := c.writePodCheckpointMarker(marker)
	require.NoError(t, err)
	require.True(t, active)
	markerInfo, err := os.Lstat(c.podCheckpointMarkerPath(marker.SandboxID))
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

	require.NoError(t, c.removePodCheckpointMarker(marker.SandboxID))
	markers, err = c.loadPodCheckpointMarkers()
	require.NoError(t, err)
	assert.Empty(t, markers)
}

func TestPodCheckpointMarkerPostPublishFailureRollsBack(t *testing.T) {
	c := &CheckpointService{rootDir: t.TempDir()}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
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
	_, statErr := os.Lstat(c.podCheckpointMarkerPath(marker.SandboxID))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestPodCheckpointMarkerPostPublishRollbackFailureStaysActive(t *testing.T) {
	c := &CheckpointService{rootDir: t.TempDir()}
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a"},
	}
	operations := defaultPodCheckpointMarkerFileOperations
	operations.syncDirectory = func(string) error { return errors.New("injected publish sync failure") }
	operations.remove = func(path string) error {
		if path == c.podCheckpointMarkerPath(marker.SandboxID) {
			return errors.New("injected rollback failure")
		}
		return os.Remove(path)
	}

	active, err := c.writePodCheckpointMarkerWithFileOperations(marker, operations)
	require.ErrorContains(t, err, "injected publish sync failure")
	require.ErrorContains(t, err, "injected rollback failure")
	assert.True(t, active)
	_, statErr := os.Lstat(c.podCheckpointMarkerPath(marker.SandboxID))
	require.NoError(t, statErr)
}

func TestValidatePodCheckpointMarker(t *testing.T) {
	valid := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
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
		{name: "version", mutate: func(marker *podCheckpointRecoveryMarker) { marker.Version++ }, wantErr: "version"},
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
	_, err = os.Stat(service.podCheckpointMarkerPath(marker.SandboxID))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = imageStore.Get(namespaces.WithNamespace(ctx, constants.K8sContainerdNamespace), imageName)
	require.True(t, errdefs.IsNotFound(err), "Kubernetes checkpoint image must be removed: %v", err)
	_, err = imageStore.Get(namespaces.WithNamespace(ctx, "other"), imageName)
	require.NoError(t, err, "recovery must preserve images in other namespaces")
}

func TestRecoverMissingCheckpointCgroup(t *testing.T) {
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
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
				_, err = os.Stat(service.podCheckpointMarkerPath(marker.SandboxID))
				require.NoError(t, err, "uncertain recovery must retain the marker")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []string{checkpointImageName("container-a"), checkpointImageName("container-b")}, imageStore.deleted)
			_, err = os.Stat(service.podCheckpointMarkerPath(marker.SandboxID))
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
	deleted []string
}

func (s *checkpointRecoveryImageStore) Delete(_ context.Context, name string, _ ...images.DeleteOpt) error {
	s.deleted = append(s.deleted, name)
	return nil
}
