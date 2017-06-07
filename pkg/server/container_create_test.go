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
	"errors"
	"os"
	"testing"

	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func TestCreateContainer(t *testing.T) {
	testSandboxID := "test-sandbox-id"
	testNameMeta := &runtime.ContainerMetadata{
		Name:    "test-name",
		Attempt: 1,
	}
	testSandboxNameMeta := &runtime.PodSandboxMetadata{
		Name:      "test-sandbox-name",
		Uid:       "test-sandbox-uid",
		Namespace: "test-sandbox-namespace",
		Attempt:   2,
	}
	// Use an image id to avoid image name resolution.
	// TODO(random-liu): Change this to image name after we have complete image
	// management unit test framework.
	testImage := "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113799"
	testChainID := "test-chain-id"
	testImageMetadata := metadata.ImageMetadata{
		ID:      testImage,
		ChainID: testChainID,
		Config:  &imagespec.ImageConfig{},
	}
	testConfig := &runtime.ContainerConfig{
		Metadata: testNameMeta,
		Image: &runtime.ImageSpec{
			Image: testImage,
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}
	testSandboxConfig := &runtime.PodSandboxConfig{
		Metadata: testSandboxNameMeta,
	}

	for desc, test := range map[string]struct {
		sandboxMetadata    *metadata.SandboxMetadata
		reserveNameErr     bool
		imageMetadataErr   bool
		prepareSnapshotErr error
		createRootDirErr   error
		createMetadataErr  bool
		expectErr          bool
		expectMeta         *metadata.ContainerMetadata
	}{
		"should return error if sandbox does not exist": {
			sandboxMetadata: nil,
			expectErr:       true,
		},
		"should return error if name is reserved": {
			sandboxMetadata: &metadata.SandboxMetadata{
				ID:     testSandboxID,
				Name:   makeSandboxName(testSandboxNameMeta),
				Config: testSandboxConfig,
			},
			reserveNameErr: true,
			expectErr:      true,
		},
		"should return error if fail to create root directory": {
			sandboxMetadata: &metadata.SandboxMetadata{
				ID:     testSandboxID,
				Name:   makeSandboxName(testSandboxNameMeta),
				Config: testSandboxConfig,
			},
			createRootDirErr: errors.New("random error"),
			expectErr:        true,
		},
		"should return error if image is not pulled": {
			sandboxMetadata: &metadata.SandboxMetadata{
				ID:     testSandboxID,
				Name:   makeSandboxName(testSandboxNameMeta),
				Config: testSandboxConfig,
			},
			imageMetadataErr: true,
			expectErr:        true,
		},
		"should return error if prepare snapshot fails": {
			sandboxMetadata: &metadata.SandboxMetadata{
				ID:     testSandboxID,
				Name:   makeSandboxName(testSandboxNameMeta),
				Config: testSandboxConfig,
			},
			prepareSnapshotErr: errors.New("random error"),
			expectErr:          true,
		},
		"should be able to create container successfully": {
			sandboxMetadata: &metadata.SandboxMetadata{
				ID:     testSandboxID,
				Name:   makeSandboxName(testSandboxNameMeta),
				Config: testSandboxConfig,
			},
			expectErr: false,
			expectMeta: &metadata.ContainerMetadata{
				Name:      makeContainerName(testNameMeta, testSandboxNameMeta),
				SandboxID: testSandboxID,
				ImageRef:  testImage,
				Config:    testConfig,
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fakeSnapshotClient := c.snapshotService.(*servertesting.FakeSnapshotClient)
		fakeOS := c.os.(*ostesting.FakeOS)
		if test.sandboxMetadata != nil {
			assert.NoError(t, c.sandboxStore.Create(*test.sandboxMetadata))
		}
		containerName := makeContainerName(testNameMeta, testSandboxNameMeta)
		if test.reserveNameErr {
			assert.NoError(t, c.containerNameIndex.Reserve(containerName, "random id"))
		}
		if !test.imageMetadataErr {
			assert.NoError(t, c.imageMetadataStore.Create(testImageMetadata))
		}
		if test.prepareSnapshotErr != nil {
			fakeSnapshotClient.InjectError("prepare", test.prepareSnapshotErr)
		}
		rootExists := false
		rootPath := ""
		fakeOS.MkdirAllFn = func(path string, perm os.FileMode) error {
			assert.Equal(t, os.FileMode(0755), perm)
			rootPath = path
			if test.createRootDirErr == nil {
				rootExists = true
			}
			return test.createRootDirErr
		}
		fakeOS.RemoveAllFn = func(path string) error {
			assert.Equal(t, rootPath, path)
			rootExists = false
			return nil
		}
		resp, err := c.CreateContainer(context.Background(), &runtime.CreateContainerRequest{
			PodSandboxId:  testSandboxID,
			Config:        testConfig,
			SandboxConfig: testSandboxConfig,
		})
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, resp)
			assert.False(t, rootExists, "root directory should be cleaned up")
			if !test.reserveNameErr {
				assert.NoError(t, c.containerNameIndex.Reserve(containerName, "random id"),
					"container name should be released")
			}
			metas, err := c.containerStore.List()
			assert.NoError(t, err)
			assert.Empty(t, metas, "container metadata should not be created")
			continue
		}
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		id := resp.GetContainerId()
		assert.True(t, rootExists)
		assert.Equal(t, getContainerRootDir(c.rootDir, id), rootPath, "root directory should be created")
		meta, err := c.containerStore.Get(id)
		assert.NoError(t, err)
		require.NotNil(t, meta)
		test.expectMeta.ID = id
		// TODO(random-liu): Use fake clock to test CreatedAt.
		test.expectMeta.CreatedAt = meta.CreatedAt
		assert.Equal(t, test.expectMeta, meta, "container metadata should be created")

		assert.Equal(t, []string{"prepare"}, fakeSnapshotClient.GetCalledNames(), "prepare should be called")
		calls := fakeSnapshotClient.GetCalledDetails()
		prepareOpts := calls[0].Argument.(*snapshotapi.PrepareRequest)
		assert.Equal(t, &snapshotapi.PrepareRequest{
			Key:    id,
			Parent: testChainID,
		}, prepareOpts, "prepare request should be correct")
	}
}
