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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	snapshotstore "github.com/containerd/containerd/v2/internal/cri/store/snapshot"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/typeurl/v2"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/server/images"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
)

func getContainerStatusTestData(t *testing.T) (*containerstore.Metadata, containerd.Container, *containerstore.Status,
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

	container := &fakeSpecOnlyContainer{t: t, spec: &specs.Spec{}}

	expected := &runtime.ContainerStatus{
		Id:          testID,
		Metadata:    config.GetMetadata(),
		State:       runtime.ContainerState_CONTAINER_CREATED,
		CreatedAt:   createdAt,
		Image:       &runtime.ImageSpec{Image: "gcr.io/library/busybox:latest"},
		ImageRef:    "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		ImageId:     imageID,
		Reason:      completeExitReason,
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
		Mounts:      config.GetMounts(),
		LogPath:     "test-log-path",
		User:        &runtime.ContainerUser{},
	}

	return metadata, container, status, image, expected
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
		t.Run(test.desc, func(t *testing.T) {

			metadata, ctnr, status, _, expected := getContainerStatusTestData(t)
			// Update status with test case.
			status.StartedAt = test.startedAt
			status.FinishedAt = test.finishedAt
			status.ExitCode = test.exitCode
			status.Reason = test.reason
			status.Message = test.message
			container, err := containerstore.NewContainer(
				*metadata,
				containerstore.WithFakeStatus(*status),
				containerstore.WithContainer(ctnr),
			)
			assert.NoError(t, err)
			// Set expectation based on test case.
			expected.Reason = test.expectedReason
			expected.StartedAt = test.startedAt
			expected.FinishedAt = test.finishedAt
			expected.ExitCode = test.exitCode
			expected.Message = test.message
			expected.StopSignal = runtime.Signal_SIGNAL_SIGTERM
			patchExceptedWithState(expected, test.expectedState)
			containerStatus, err := toCRIContainerStatus(context.Background(),
				container,
				expected.Image,
				expected.ImageRef,
				expected.ImageId)
			assert.Nil(t, err)
			assert.Equal(t, expected, containerStatus, test.desc)
		})
	}
}

// TODO(mikebrow): add a fake containerd container.Container.Spec client api so we can test verbose is true option
func TestToCRIContainerInfo(t *testing.T) {
	metadata, _, status, _, _ := getContainerStatusTestData(t)
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
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			metadata, ctnr, status, image, expected := getContainerStatusTestData(t)
			// Update status with test case.
			status.StartedAt = test.startedAt
			status.FinishedAt = test.finishedAt
			status.Reason = test.reason
			container, err := containerstore.NewContainer(
				*metadata,
				containerstore.WithFakeStatus(*status),
				containerstore.WithContainer(ctnr),
			)
			assert.NoError(t, err)
			if test.exist {
				assert.NoError(t, c.containerStore.Add(container))
			}
			if test.imageExist {
				imageStore, err := imagestore.NewFakeStore([]imagestore.Image{*image})
				assert.NoError(t, err)
				c.ImageService = &fakeImageService{imageStore: imageStore}
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
			expected.StopSignal = runtime.Signal_SIGNAL_SIGTERM
			patchExceptedWithState(expected, test.expectedState)
			assert.Equal(t, expected, resp.GetStatus())
		})
	}
}

func TestToCRISignal(t *testing.T) {
	tests := []struct {
		input    string
		expected runtime.Signal
	}{
		{input: "SIGTERM", expected: runtime.Signal_SIGNAL_SIGTERM},
		{input: "SIGRTMIN+1", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS1},
		{input: "SIGRTMAX-1", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS1},
		{input: "SIGNAL_SIGABRT", expected: runtime.Signal_SIGNAL_SIGABRT},
		{input: "SIGNAL_SIGALRM", expected: runtime.Signal_SIGNAL_SIGALRM},
		{input: "SIGNAL_SIGBUS", expected: runtime.Signal_SIGNAL_SIGBUS},
		{input: "SIGNAL_SIGCHLD", expected: runtime.Signal_SIGNAL_SIGCHLD},
		{input: "SIGNAL_SIGCLD", expected: runtime.Signal_SIGNAL_SIGCLD},
		{input: "SIGNAL_SIGCONT", expected: runtime.Signal_SIGNAL_SIGCONT},
		{input: "SIGNAL_SIGFPE", expected: runtime.Signal_SIGNAL_SIGFPE},
		{input: "SIGNAL_SIGHUP", expected: runtime.Signal_SIGNAL_SIGHUP},
		{input: "SIGNAL_SIGILL", expected: runtime.Signal_SIGNAL_SIGILL},
		{input: "SIGNAL_SIGINT", expected: runtime.Signal_SIGNAL_SIGINT},
		{input: "SIGNAL_SIGIO", expected: runtime.Signal_SIGNAL_SIGIO},
		{input: "SIGNAL_SIGIOT", expected: runtime.Signal_SIGNAL_SIGIOT},
		{input: "SIGNAL_SIGKILL", expected: runtime.Signal_SIGNAL_SIGKILL},
		{input: "SIGNAL_SIGPIPE", expected: runtime.Signal_SIGNAL_SIGPIPE},
		{input: "SIGNAL_SIGPOLL", expected: runtime.Signal_SIGNAL_SIGPOLL},
		{input: "SIGNAL_SIGPROF", expected: runtime.Signal_SIGNAL_SIGPROF},
		{input: "SIGNAL_SIGPWR", expected: runtime.Signal_SIGNAL_SIGPWR},
		{input: "SIGNAL_SIGQUIT", expected: runtime.Signal_SIGNAL_SIGQUIT},
		{input: "SIGNAL_SIGSEGV", expected: runtime.Signal_SIGNAL_SIGSEGV},
		{input: "SIGNAL_SIGSTKFLT", expected: runtime.Signal_SIGNAL_SIGSTKFLT},
		{input: "SIGNAL_SIGSTOP", expected: runtime.Signal_SIGNAL_SIGSTOP},
		{input: "SIGNAL_SIGSYS", expected: runtime.Signal_SIGNAL_SIGSYS},
		{input: "SIGNAL_SIGTERM", expected: runtime.Signal_SIGNAL_SIGTERM},
		{input: "SIGNAL_SIGTRAP", expected: runtime.Signal_SIGNAL_SIGTRAP},
		{input: "SIGNAL_SIGTSTP", expected: runtime.Signal_SIGNAL_SIGTSTP},
		{input: "SIGNAL_SIGTTIN", expected: runtime.Signal_SIGNAL_SIGTTIN},
		{input: "SIGNAL_SIGTTOU", expected: runtime.Signal_SIGNAL_SIGTTOU},
		{input: "SIGNAL_SIGURG", expected: runtime.Signal_SIGNAL_SIGURG},
		{input: "SIGNAL_SIGUSR1", expected: runtime.Signal_SIGNAL_SIGUSR1},
		{input: "SIGNAL_SIGUSR2", expected: runtime.Signal_SIGNAL_SIGUSR2},
		{input: "SIGNAL_SIGVTALRM", expected: runtime.Signal_SIGNAL_SIGVTALRM},
		{input: "SIGNAL_SIGWINCH", expected: runtime.Signal_SIGNAL_SIGWINCH},
		{input: "SIGNAL_SIGXCPU", expected: runtime.Signal_SIGNAL_SIGXCPU},
		{input: "SIGNAL_SIGXFSZ", expected: runtime.Signal_SIGNAL_SIGXFSZ},
		{input: "SIGNAL_SIGRTMIN", expected: runtime.Signal_SIGNAL_SIGRTMIN},
		{input: "SIGNAL_SIGRTMIN+1", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS1},
		{input: "SIGNAL_SIGRTMIN+2", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS2},
		{input: "SIGNAL_SIGRTMIN+3", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS3},
		{input: "SIGNAL_SIGRTMIN+4", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS4},
		{input: "SIGNAL_SIGRTMIN+5", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS5},
		{input: "SIGNAL_SIGRTMIN+6", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS6},
		{input: "SIGNAL_SIGRTMIN+7", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS7},
		{input: "SIGNAL_SIGRTMIN+8", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS8},
		{input: "SIGNAL_SIGRTMIN+9", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS9},
		{input: "SIGNAL_SIGRTMIN+10", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS10},
		{input: "SIGNAL_SIGRTMIN+11", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS11},
		{input: "SIGNAL_SIGRTMIN+12", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS12},
		{input: "SIGNAL_SIGRTMIN+13", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS13},
		{input: "SIGNAL_SIGRTMIN+14", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS14},
		{input: "SIGNAL_SIGRTMIN+15", expected: runtime.Signal_SIGNAL_SIGRTMINPLUS15},
		{input: "SIGNAL_SIGRTMAX-14", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS14},
		{input: "SIGNAL_SIGRTMAX-13", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS13},
		{input: "SIGNAL_SIGRTMAX-12", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS12},
		{input: "SIGNAL_SIGRTMAX-11", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS11},
		{input: "SIGNAL_SIGRTMAX-10", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS10},
		{input: "SIGNAL_SIGRTMAX-9", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS9},
		{input: "SIGNAL_SIGRTMAX-8", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS8},
		{input: "SIGNAL_SIGRTMAX-7", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS7},
		{input: "SIGNAL_SIGRTMAX-6", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS6},
		{input: "SIGNAL_SIGRTMAX-5", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS5},
		{input: "SIGNAL_SIGRTMAX-4", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS4},
		{input: "SIGNAL_SIGRTMAX-3", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS3},
		{input: "SIGNAL_SIGRTMAX-2", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS2},
		{input: "SIGNAL_SIGRTMAX-1", expected: runtime.Signal_SIGNAL_SIGRTMAXMINUS1},
		{input: "SIGNAL_SIGRTMAX", expected: runtime.Signal_SIGNAL_SIGRTMAX},
		{input: "SIGNAL_SIGNOPE", expected: runtime.Signal_SIGNAL_RUNTIME_DEFAULT},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			assert.Equal(t, test.expected, toCRISignal(test.input))
		})
	}
}

type fakeImageService struct {
	imageStore   *imagestore.Store
	pinnedImages map[string]string
}

func (s *fakeImageService) RuntimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string {
	return ""
}

func (s *fakeImageService) UpdateImage(ctx context.Context, r string) error { return nil }

func (s *fakeImageService) CheckImages(ctx context.Context) error { return nil }

func (s *fakeImageService) GetImage(id string) (imagestore.Image, error) { return s.imageStore.Get(id) }

func (s *fakeImageService) GetSnapshot(key, snapshotter string) (snapshotstore.Snapshot, error) {
	return snapshotstore.Snapshot{}, errors.New("not implemented")
}

func (s *fakeImageService) PinnedImage(name string) string { return s.pinnedImages[name] }

func (s *fakeImageService) LocalResolve(refOrID string) (imagestore.Image, error) {
	return imagestore.Image{}, errors.New("not implemented")
}

func (s *fakeImageService) ImageFSPaths() map[string]string { return make(map[string]string) }

func (s *fakeImageService) Config() criconfig.ImageConfig {
	return criconfig.ImageConfig{}
}

func (s *fakeImageService) PullImage(context.Context, string, func(string) (string, string, error), *runtime.PodSandboxConfig, string) (string, error) {
	return "", errors.New("not implemented")
}

func (s *fakeImageService) UpdateRuntimeSnapshotter(runtimeName string, imagePlatform images.ImagePlatform) {
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

var _ containerd.Container = &fakeSpecOnlyContainer{}

type fakeSpecOnlyContainer struct {
	t         *testing.T
	spec      *specs.Spec
	errOnSpec error
}

// Spec implements client.Container.
func (c *fakeSpecOnlyContainer) Spec(context.Context) (*specs.Spec, error) {
	if c.errOnSpec != nil {
		return nil, c.errOnSpec
	}
	return c.spec, nil
}

// Checkpoint implements client.Container.
func (c *fakeSpecOnlyContainer) Checkpoint(context.Context, string, ...containerd.CheckpointOpts) (containerd.Image, error) {
	c.t.Error("fakeSpecOnlyContainer.Checkpoint: not implemented")
	return nil, errors.New("not implemented")
}

// Delete implements client.Container.
func (c *fakeSpecOnlyContainer) Delete(context.Context, ...containerd.DeleteOpts) error {
	c.t.Error("fakeSpecOnlyContainer.Delete: not implemented")
	return errors.New("not implemented")
}

// Extensions implements client.Container.
func (c *fakeSpecOnlyContainer) Extensions(context.Context) (map[string]typeurl.Any, error) {
	c.t.Error("fakeSpecOnlyContainer.Extensions: not implemented")
	return nil, errors.New("not implemented")
}

// ID implements client.Container.
func (c *fakeSpecOnlyContainer) ID() string {
	c.t.Error("fakeSpecOnlyContainer.ID: not implemented")
	return "" // not implemented
}

// Image implements client.Container.
func (c *fakeSpecOnlyContainer) Image(context.Context) (containerd.Image, error) {
	c.t.Error("fakeSpecOnlyContainer.Image: not implemented")
	return nil, errors.New("not implemented")
}

// Info implements client.Container.
func (c *fakeSpecOnlyContainer) Info(context.Context, ...containerd.InfoOpts) (containers.Container, error) {
	c.t.Error("fakeSpecOnlyContainer.Info: not implemented")
	return containers.Container{}, errors.New("not implemented")
}

// Labels implements client.Container.
func (c *fakeSpecOnlyContainer) Labels(context.Context) (map[string]string, error) {
	c.t.Error("fakeSpecOnlyContainer.Labels: not implemented")
	return nil, errors.New("not implemented")
}

// NewTask implements client.Container.
func (c *fakeSpecOnlyContainer) NewTask(context.Context, cio.Creator, ...containerd.NewTaskOpts) (containerd.Task, error) {
	c.t.Error("fakeSpecOnlyContainer.NewTask: not implemented")
	return nil, errors.New("not implemented")
}

// SetLabels implements client.Container.
func (c *fakeSpecOnlyContainer) SetLabels(context.Context, map[string]string) (map[string]string, error) {
	c.t.Error("fakeSpecOnlyContainer.SetLabels: not implemented")
	return nil, errors.New("not implemented")
}

// Task implements client.Container.
func (c *fakeSpecOnlyContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	c.t.Error("fakeSpecOnlyContainer.Task: not implemented")
	return nil, errors.New("not implemented")
}

// Update implements client.Container.
func (c *fakeSpecOnlyContainer) Update(context.Context, ...containerd.UpdateContainerOpts) error {
	c.t.Error("fakeSpecOnlyContainer.Update: not implemented")
	return errors.New("not implemented")
}

// Restore implements client.Container.
func (c *fakeSpecOnlyContainer) Restore(context.Context, cio.Creator, string) (int, error) {
	c.t.Error("fakeSpecOnlyContainer.Restore: not implemented")
	return -1, errors.New("not implemented")
}
