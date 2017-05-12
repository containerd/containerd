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

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/types/container"

	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func TestStopPodSandbox(t *testing.T) {
	testID := "test-id"
	testSandbox := metadata.SandboxMetadata{
		ID:   testID,
		Name: "test-name",
	}
	testContainer := container.Container{
		ID:     testID,
		Pid:    1,
		Status: container.Status_RUNNING,
	}

	for desc, test := range map[string]struct {
		sandboxContainers []container.Container
		injectSandbox     bool
		injectErr         error
		expectErr         bool
		expectCalls       []string
	}{
		"stop non-existing sandbox": {
			injectSandbox: false,
			expectErr:     true,
			expectCalls:   []string{},
		},
		"stop sandbox with sandbox container": {
			sandboxContainers: []container.Container{testContainer},
			injectSandbox:     true,
			expectErr:         false,
			expectCalls:       []string{"delete"},
		},
		"stop sandbox with sandbox container not exist error": {
			sandboxContainers: []container.Container{},
			injectSandbox:     true,
			// Inject error to make sure fake execution client returns error.
			injectErr:   grpc.Errorf(codes.Unknown, containerd.ErrContainerNotExist.Error()),
			expectErr:   false,
			expectCalls: []string{"delete"},
		},
		"stop sandbox with with arbitrary error": {
			injectSandbox: true,
			injectErr:     grpc.Errorf(codes.Unknown, "arbitrary error"),
			expectErr:     true,
			expectCalls:   []string{"delete"},
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := c.containerService.(*servertesting.FakeExecutionClient)
		fake.SetFakeContainers(test.sandboxContainers)

		if test.injectSandbox {
			assert.NoError(t, c.sandboxStore.Create(testSandbox))
			c.sandboxIDIndex.Add(testID)
		}
		if test.injectErr != nil {
			fake.InjectError("delete", test.injectErr)
		}

		res, err := c.StopPodSandbox(context.Background(), &runtime.StopPodSandboxRequest{
			PodSandboxId: testID,
		})
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, res)
		} else {
			assert.NoError(t, err)
			assert.NotNil(t, res)
		}
		assert.Equal(t, test.expectCalls, fake.GetCalledNames())
	}
}
