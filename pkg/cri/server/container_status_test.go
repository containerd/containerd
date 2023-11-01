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
	"errors"
	"testing"
	"time"

	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	snapshotstore "github.com/containerd/containerd/v2/pkg/cri/store/snapshot"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
)

func getContainerStatusTestData() (*containerstore.Metadata, *containerstore.Status,
	*imagestore.Image, *runtime.ContainerStatus) {
	imageID := "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testID := "test-id"
	config := &runtime.ContainerConfig{
		Metadata: &runtime.ContainerMetadata{
			Name:    "test-name",
			Attempt: 1,
		},
		Image: &runtime.ImageSpec{Image: "test-image"},
		Mounts: []*runtime.Mount{{
			ContainerPath: "test-container-path",
			HostPath:      "test-host-path",
		}},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}

	createdAt := time.Now().UnixNano()

	metadata := &containerstore.Metadata{
		ID:        testID,
		Name:      "test-long-name",
		SandboxID: "test-sandbox-id",
		Config:    config,
		ImageRef:  imageID,
		LogPath:   "test-log-path",
	}
	status := &containerstore.Status{
		Pid:       1234,
		CreatedAt: createdAt,
	}
	image := &imagestore.Image{
		ID: imageID,
		References: []string{
			"gcr.io/library/busybox:latest",
			"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
	}
	expected := &runtime.ContainerStatus{
		Id:          testID,
		Metadata:    config.GetMetadata(),
		State:       runtime.ContainerState_CONTAINER_CREATED,
		CreatedAt:   createdAt,
		Image:       &runtime.ImageSpec{Image: "gcr.io/library/busybox:latest"},
		ImageRef:    "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		Reason:      completeExitReason,
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
		Mounts:      config.GetMounts(),
		LogPath:     "test-log-path",
	}

	return metadata, status, image, expected
}

func TestToCRIContainerStatus(t *testing.T) {
	for _, test := range []struct {
		desc           string
		startedAt      int64
		finishedAt     int64
		exitCode       int32
		reason         string
		message        string
		expectedState  runtime.ContainerState
		expectedReason string
	}{
		{
			desc:          "container created",
			expectedState: runtime.ContainerState_CONTAINER_CREATED,
		},
		{
			desc:          "container running",
			startedAt:     time.Now().UnixNano(),
			expectedState: runtime.ContainerState_CONTAINER_RUNNING,
		},
		{
			desc:           "container exited with reason",
			startedAt:      time.Now().UnixNano(),
			finishedAt:     time.Now().UnixNano(),
			exitCode:       1,
			reason:         "test-reason",
			message:        "test-message",
			expectedState:  runtime.ContainerState_CONTAINER_EXITED,
			expectedReason: "test-reason",
		},
		{
			desc:           "container exited with exit code 0 without reason",
			startedAt:      time.Now().UnixNano(),
			finishedAt:     time.Now().UnixNano(),
			exitCode:       0,
			message:        "test-message",
			expectedState:  runtime.ContainerState_CONTAINER_EXITED,
			expectedReason: completeExitReason,
		},
		{
			desc:           "container exited with non-zero exit code without reason",
			startedAt:      time.Now().UnixNano(),
			finishedAt:     time.Now().UnixNano(),
			exitCode:       1,
			message:        "test-message",
			expectedState:  runtime.ContainerState_CONTAINER_EXITED,
			expectedReason: errorExitReason,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {

			metadata, status, _, expected := getContainerStatusTestData()
			// Update status with test case.
			status.StartedAt = test.startedAt
			status.FinishedAt = test.finishedAt
			status.ExitCode = test.exitCode
			status.Reason = test.reason
			status.Message = test.message
			container, err := containerstore.NewContainer(
				*metadata,
				containerstore.WithFakeStatus(*status),
			)
			assert.NoError(t, err)
			// Set expectation based on test case.
			expected.Reason = test.expectedReason
			expected.StartedAt = test.startedAt
			expected.FinishedAt = test.finishedAt
			expected.ExitCode = test.exitCode
			expected.Message = test.message
			patchExceptedWithState(expected, test.expectedState)
			containerStatus := toCRIContainerStatus(container,
				expected.Image,
				expected.ImageRef)
			assert.Equal(t, expected, containerStatus, test.desc)
		})
	}
}

// TODO(mikebrow): add a fake containerd container.Container.Spec client api so we can test verbose is true option
func TestToCRIContainerInfo(t *testing.T) {
	metadata, status, _, _ := getContainerStatusTestData()
	container, err := containerstore.NewContainer(
		*metadata,
		containerstore.WithFakeStatus(*status),
	)
	assert.NoError(t, err)

	info, err := toCRIContainerInfo(context.Background(),
		container,
		false)
	assert.NoError(t, err)
	assert.Nil(t, info)
}

func TestContainerStatus(t *testing.T) {
	for _, test := range []struct {
		desc          string
		exist         bool
		imageExist    bool
		startedAt     int64
		finishedAt    int64
		reason        string
		expectedState runtime.ContainerState
		expectErr     bool
	}{
		{
			desc:          "container created",
			exist:         true,
			imageExist:    true,
			expectedState: runtime.ContainerState_CONTAINER_CREATED,
		},
		{
			desc:          "container running",
			exist:         true,
			imageExist:    true,
			startedAt:     time.Now().UnixNano(),
			expectedState: runtime.ContainerState_CONTAINER_RUNNING,
		},
		{
			desc:          "container exited",
			exist:         true,
			imageExist:    true,
			startedAt:     time.Now().UnixNano(),
			finishedAt:    time.Now().UnixNano(),
			reason:        "test-reason",
			expectedState: runtime.ContainerState_CONTAINER_EXITED,
		},
		{
			desc:       "container not exist",
			exist:      false,
			imageExist: true,
			expectErr:  true,
		},
		{
			desc:       "image not exist",
			exist:      false,
			imageExist: false,
			expectErr:  true,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			metadata, status, image, expected := getContainerStatusTestData()
			// Update status with test case.
			status.StartedAt = test.startedAt
			status.FinishedAt = test.finishedAt
			status.Reason = test.reason
			container, err := containerstore.NewContainer(
				*metadata,
				containerstore.WithFakeStatus(*status),
			)
			assert.NoError(t, err)
			if test.exist {
				assert.NoError(t, c.containerStore.Add(container))
			}
			if test.imageExist {
				imageStore, err := imagestore.NewFakeStore([]imagestore.Image{*image})
				assert.NoError(t, err)
				c.imageService = &fakeImageService{imageStore: imageStore}
			}
			resp, err := c.ContainerStatus(context.Background(), &runtime.ContainerStatusRequest{ContainerId: container.ID})
			if test.expectErr {
				assert.Error(t, err)
				assert.Nil(t, resp)
				return
			}
			// Set expectation based on test case.
			expected.StartedAt = test.startedAt
			expected.FinishedAt = test.finishedAt
			expected.Reason = test.reason
			patchExceptedWithState(expected, test.expectedState)
			assert.Equal(t, expected, resp.GetStatus())
		})
	}
}

type fakeImageService struct {
	runtime.ImageServiceServer
	imageStore *imagestore.Store
}

func (s *fakeImageService) RuntimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string {
	return ""
}

func (s *fakeImageService) UpdateImage(ctx context.Context, r string) error { return nil }

func (s *fakeImageService) GetImage(id string) (imagestore.Image, error) { return s.imageStore.Get(id) }

func (s *fakeImageService) GetSnapshot(key, snapshotter string) (snapshotstore.Snapshot, error) {
	return snapshotstore.Snapshot{}, errors.New("not implemented")
}

func (s *fakeImageService) LocalResolve(refOrID string) (imagestore.Image, error) {
	return imagestore.Image{}, errors.New("not implemented")
}

func patchExceptedWithState(expected *runtime.ContainerStatus, state runtime.ContainerState) {
	expected.State = state
	switch state {
	case runtime.ContainerState_CONTAINER_CREATED:
		expected.StartedAt, expected.FinishedAt = 0, 0
	case runtime.ContainerState_CONTAINER_RUNNING:
		expected.FinishedAt = 0
	}
}
