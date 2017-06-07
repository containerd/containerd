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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"

	"github.com/containerd/containerd/api/types/task"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"

	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"
)

func TestRemovePodSandbox(t *testing.T) {
	testID := "test-id"
	testName := "test-name"
	testMetadata := metadata.SandboxMetadata{
		ID:   testID,
		Name: testName,
	}
	for desc, test := range map[string]struct {
		sandboxContainers   []task.Task
		injectMetadata      bool
		injectContainerdErr error
		injectFSErr         error
		expectErr           bool
		expectRemoved       string
		expectCalls         []string
	}{
		"should not return error if sandbox does not exist": {
			injectMetadata: false,
			expectErr:      false,
			expectCalls:    []string{},
		},
		"should return error when sandbox container is not deleted": {
			injectMetadata:    true,
			sandboxContainers: []task.Task{{ID: testID}},
			expectErr:         true,
			expectCalls:       []string{"info"},
		},
		"should return error when arbitrary containerd error is injected": {
			injectMetadata:      true,
			injectContainerdErr: fmt.Errorf("arbitrary error"),
			expectErr:           true,
			expectCalls:         []string{"info"},
		},
		"should return error when error fs error is injected": {
			injectMetadata: true,
			injectFSErr:    fmt.Errorf("fs error"),
			expectRemoved:  getSandboxRootDir(testRootDir, testID),
			expectErr:      true,
			expectCalls:    []string{"info"},
		},
		"should be able to successfully delete": {
			injectMetadata: true,
			expectRemoved:  getSandboxRootDir(testRootDir, testID),
			expectCalls:    []string{"info"},
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := c.containerService.(*servertesting.FakeExecutionClient)
		fakeOS := c.os.(*ostesting.FakeOS)
		fake.SetFakeContainers(test.sandboxContainers)
		if test.injectMetadata {
			c.sandboxNameIndex.Reserve(testName, testID)
			c.sandboxIDIndex.Add(testID)
			c.sandboxStore.Create(testMetadata)
		}
		if test.injectContainerdErr != nil {
			fake.InjectError("info", test.injectContainerdErr)
		}
		fakeOS.RemoveAllFn = func(path string) error {
			assert.Equal(t, test.expectRemoved, path)
			return test.injectFSErr
		}
		res, err := c.RemovePodSandbox(context.Background(), &runtime.RemovePodSandboxRequest{
			PodSandboxId: testID,
		})
		assert.Equal(t, test.expectCalls, fake.GetCalledNames())
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, res)
			continue
		}
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.NoError(t, c.sandboxNameIndex.Reserve(testName, testID),
			"sandbox name should be released")
		_, err = c.sandboxIDIndex.Get(testID)
		assert.Error(t, err, "sandbox id should be removed")
		meta, err := c.sandboxStore.Get(testID)
		assert.Error(t, err)
		assert.True(t, metadata.IsNotExistError(err))
		assert.Nil(t, meta, "sandbox metadata should be removed")
		res, err = c.RemovePodSandbox(context.Background(), &runtime.RemovePodSandboxRequest{
			PodSandboxId: testID,
		})
		assert.NoError(t, err)
		assert.NotNil(t, res, "remove should be idempotent")
	}
}
