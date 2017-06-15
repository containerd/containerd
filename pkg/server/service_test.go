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
	"io"
	"os"
	"testing"

	"github.com/containerd/containerd/api/services/execution"
	snapshotservice "github.com/containerd/containerd/services/snapshot"
	"github.com/docker/docker/pkg/truncindex"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	"github.com/kubernetes-incubator/cri-containerd/pkg/registrar"
	agentstesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/agents/testing"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

type nopReadWriteCloser struct{}

// Return error directly to avoid read/write.
func (nopReadWriteCloser) Read(p []byte) (n int, err error)  { return 0, io.EOF }
func (nopReadWriteCloser) Write(p []byte) (n int, err error) { return 0, io.ErrShortWrite }
func (nopReadWriteCloser) Close() error                      { return nil }

const (
	testRootDir = "/test/rootfs"
	// Use an image id as test sandbox image to avoid image name resolve.
	// TODO(random-liu): Change this to image name after we have complete image
	// management unit test framework.
	testSandboxImage = "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113798"
)

// newTestCRIContainerdService creates a fake criContainerdService for test.
func newTestCRIContainerdService() *criContainerdService {
	return &criContainerdService{
		os:                 ostesting.NewFakeOS(),
		rootDir:            testRootDir,
		sandboxImage:       testSandboxImage,
		sandboxStore:       metadata.NewSandboxStore(store.NewMetadataStore()),
		imageMetadataStore: metadata.NewImageMetadataStore(store.NewMetadataStore()),
		sandboxNameIndex:   registrar.NewRegistrar(),
		sandboxIDIndex:     truncindex.NewTruncIndex(nil),
		containerStore:     metadata.NewContainerStore(store.NewMetadataStore()),
		containerNameIndex: registrar.NewRegistrar(),
		containerService:   servertesting.NewFakeExecutionClient(),
		netPlugin:          servertesting.NewFakeCNIPlugin(),
		agentFactory:       agentstesting.NewFakeAgentFactory(),
	}
}

// WithFakeSnapshotClient add and return fake snapshot client.
func WithFakeSnapshotClient(c *criContainerdService) *servertesting.FakeSnapshotClient {
	fake := servertesting.NewFakeSnapshotClient()
	c.snapshotService = snapshotservice.NewSnapshotterFromClient(fake)
	return fake
}

// Test all sandbox operations.
func TestSandboxOperations(t *testing.T) {
	c := newTestCRIContainerdService()
	fake := c.containerService.(*servertesting.FakeExecutionClient)
	fakeOS := c.os.(*ostesting.FakeOS)
	fakeCNIPlugin := c.netPlugin.(*servertesting.FakeCNIPlugin)
	WithFakeSnapshotClient(c)
	fakeOS.OpenFifoFn = func(ctx context.Context, fn string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
		return nopReadWriteCloser{}, nil
	}
	// Insert sandbox image metadata.
	assert.NoError(t, c.imageMetadataStore.Create(metadata.ImageMetadata{
		ID:      testSandboxImage,
		ChainID: "test-chain-id",
		Config:  &imagespec.ImageConfig{Entrypoint: []string{"/pause"}},
	}))

	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-name",
			Uid:       "test-uid",
			Namespace: "test-ns",
			Attempt:   1,
		},
		Hostname:     "test-hostname",
		LogDirectory: "test-log-directory",
		Labels:       map[string]string{"a": "b"},
		Annotations:  map[string]string{"c": "d"},
	}

	t.Logf("should be able to run a pod sandbox")
	runRes, err := c.RunPodSandbox(context.Background(), &runtime.RunPodSandboxRequest{Config: config})
	assert.NoError(t, err)
	require.NotNil(t, runRes)
	id := runRes.GetPodSandboxId()

	t.Logf("should be able to get pod sandbox status")
	info, err := fake.Info(context.Background(), &execution.InfoRequest{ContainerID: id})
	netns := getNetworkNamespace(info.Task.Pid)
	assert.NoError(t, err)
	expectSandboxStatus := &runtime.PodSandboxStatus{
		Id:       id,
		Metadata: config.GetMetadata(),
		// TODO(random-liu): [P2] Use fake clock for CreatedAt.
		Network: &runtime.PodSandboxNetworkStatus{},
		Linux: &runtime.LinuxPodSandboxStatus{
			Namespaces: &runtime.Namespace{
				Options: &runtime.NamespaceOption{
					HostNetwork: false,
					HostPid:     false,
					HostIpc:     false,
				},
			},
		},
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
	}
	statusRes, err := c.PodSandboxStatus(context.Background(), &runtime.PodSandboxStatusRequest{PodSandboxId: id})
	assert.NoError(t, err)
	require.NotNil(t, statusRes)
	status := statusRes.GetStatus()
	expectSandboxStatus.CreatedAt = status.GetCreatedAt()
	ip, err := fakeCNIPlugin.GetContainerNetworkStatus(netns, config.GetMetadata().GetNamespace(), config.GetMetadata().GetName(), id)
	assert.NoError(t, err)
	expectSandboxStatus.Network.Ip = ip
	assert.Equal(t, expectSandboxStatus, status)

	t.Logf("should be able to list pod sandboxes")
	expectSandbox := &runtime.PodSandbox{
		Id:          id,
		Metadata:    config.GetMetadata(),
		State:       runtime.PodSandboxState_SANDBOX_NOTREADY, // TODO(mikebrow) converting to client... should this be ready?
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
	}
	listRes, err := c.ListPodSandbox(context.Background(), &runtime.ListPodSandboxRequest{})
	assert.NoError(t, err)
	require.NotNil(t, listRes)
	sandboxes := listRes.GetItems()
	assert.Len(t, sandboxes, 1)
	expectSandbox.CreatedAt = sandboxes[0].CreatedAt
	assert.Equal(t, expectSandbox, sandboxes[0])

	t.Logf("should be able to stop a pod sandbox")
	stopRes, err := c.StopPodSandbox(context.Background(), &runtime.StopPodSandboxRequest{PodSandboxId: id})
	assert.NoError(t, err)
	require.NotNil(t, stopRes)
	statusRes, err = c.PodSandboxStatus(context.Background(), &runtime.PodSandboxStatusRequest{PodSandboxId: id})
	assert.NoError(t, err)
	require.NotNil(t, statusRes)
	assert.Equal(t, runtime.PodSandboxState_SANDBOX_NOTREADY, statusRes.GetStatus().GetState(),
		"sandbox status should be NOTREADY after stopped")
	listRes, err = c.ListPodSandbox(context.Background(), &runtime.ListPodSandboxRequest{})
	assert.NoError(t, err)
	require.NotNil(t, listRes)
	assert.Len(t, listRes.GetItems(), 1)
	assert.Equal(t, runtime.PodSandboxState_SANDBOX_NOTREADY, listRes.GetItems()[0].State,
		"sandbox in list should be NOTREADY after stopped")

	t.Logf("should be able to remove a pod sandbox")
	removeRes, err := c.RemovePodSandbox(context.Background(), &runtime.RemovePodSandboxRequest{PodSandboxId: id})
	assert.NoError(t, err)
	require.NotNil(t, removeRes)
	_, err = c.PodSandboxStatus(context.Background(), &runtime.PodSandboxStatusRequest{PodSandboxId: id})
	assert.Error(t, err, "should not be able to get sandbox status after removed")
	listRes, err = c.ListPodSandbox(context.Background(), &runtime.ListPodSandboxRequest{})
	assert.NoError(t, err)
	require.NotNil(t, listRes)
	assert.Empty(t, listRes.GetItems(), "should not be able to list the sandbox after removed")

	t.Logf("should be able to create the sandbox again")
	runRes, err = c.RunPodSandbox(context.Background(), &runtime.RunPodSandboxRequest{Config: config})
	assert.NoError(t, err)
	require.NotNil(t, runRes)
}
