/*
Copyright 2017 The Kubernetes Authors.

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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
	imagestore "github.com/kubernetes-incubator/cri-containerd/pkg/store/image"
)

func getContainerStatusTestData() (*containerstore.Metadata, *containerstore.Status,
	*imagestore.Image, *runtime.ContainerStatus) {
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
	startedAt := time.Now().UnixNano()

	metadata := &containerstore.Metadata{
		ID:        testID,
		Name:      "test-long-name",
		SandboxID: "test-sandbox-id",
		Config:    config,
		ImageRef:  "test-image-id",
		LogPath:   "test-log-path",
	}
	status := &containerstore.Status{
		Pid:       1234,
		CreatedAt: createdAt,
		StartedAt: startedAt,
	}
	image := &imagestore.Image{
		ID:          "test-image-id",
		RepoTags:    []string{"test-image-repo-tag"},
		RepoDigests: []string{"test-image-repo-digest"},
	}
	expected := &runtime.ContainerStatus{
		Id:          testID,
		Metadata:    config.GetMetadata(),
		State:       runtime.ContainerState_CONTAINER_RUNNING,
		CreatedAt:   createdAt,
		StartedAt:   startedAt,
		Image:       &runtime.ImageSpec{Image: "test-image-repo-tag"},
		ImageRef:    "test-image-repo-digest",
		Reason:      completeExitReason,
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
		Mounts:      config.GetMounts(),
		LogPath:     "test-log-path",
	}

	return metadata, status, image, expected
}

func TestToCRIContainerStatus(t *testing.T) {
	for desc, test := range map[string]struct {
		finishedAt     int64
		exitCode       int32
		reason         string
		message        string
		expectedState  runtime.ContainerState
		expectedReason string
	}{
		"container running": {
			expectedState: runtime.ContainerState_CONTAINER_RUNNING,
		},
		"container exited with reason": {
			finishedAt:     time.Now().UnixNano(),
			exitCode:       1,
			reason:         "test-reason",
			message:        "test-message",
			expectedState:  runtime.ContainerState_CONTAINER_EXITED,
			expectedReason: "test-reason",
		},
		"container exited with exit code 0 without reason": {
			finishedAt:     time.Now().UnixNano(),
			exitCode:       0,
			message:        "test-message",
			expectedState:  runtime.ContainerState_CONTAINER_EXITED,
			expectedReason: completeExitReason,
		},
		"container exited with non-zero exit code without reason": {
			finishedAt:     time.Now().UnixNano(),
			exitCode:       1,
			message:        "test-message",
			expectedState:  runtime.ContainerState_CONTAINER_EXITED,
			expectedReason: errorExitReason,
		},
	} {
		metadata, status, image, expected := getContainerStatusTestData()
		// Update status with test case.
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
		expected.State = test.expectedState
		expected.Reason = test.expectedReason
		expected.FinishedAt = test.finishedAt
		expected.ExitCode = test.exitCode
		expected.Message = test.message
		assert.Equal(t, expected, toCRIContainerStatus(container,
			&runtime.ImageSpec{Image: image.RepoTags[0]},
			image.RepoDigests[0]), desc)
	}
}

func TestContainerStatus(t *testing.T) {
	for desc, test := range map[string]struct {
		exist         bool
		imageExist    bool
		finishedAt    int64
		reason        string
		expectedState runtime.ContainerState
		expectErr     bool
	}{
		"container running": {
			exist:         true,
			imageExist:    true,
			expectedState: runtime.ContainerState_CONTAINER_RUNNING,
		},
		"container exited": {
			exist:         true,
			imageExist:    true,
			finishedAt:    time.Now().UnixNano(),
			reason:        "test-reason",
			expectedState: runtime.ContainerState_CONTAINER_EXITED,
		},
		"container not exist": {
			exist:      false,
			imageExist: true,
			expectErr:  true,
		},
		"image not exist": {
			exist:      false,
			imageExist: false,
			expectErr:  true,
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		metadata, status, image, expected := getContainerStatusTestData()
		// Update status with test case.
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
			c.imageStore.Add(*image)
		}
		resp, err := c.ContainerStatus(context.Background(), &runtime.ContainerStatusRequest{ContainerId: container.ID})
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, resp)
			continue
		}
		// Set expectation based on test case.
		expected.FinishedAt = test.finishedAt
		expected.Reason = test.reason
		expected.State = test.expectedState
		assert.Equal(t, expected, resp.GetStatus())
	}
}
