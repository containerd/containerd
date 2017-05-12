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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"

	"github.com/containerd/containerd/api/types/container"

	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

// Variables used in the following test.

const sandboxStatusTestID = "test-id"

func getSandboxStatusTestData() (*metadata.SandboxMetadata, *runtime.PodSandboxStatus) {
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-name",
			Uid:       "test-uid",
			Namespace: "test-ns",
			Attempt:   1,
		},
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					HostNetwork: true,
					HostPid:     false,
					HostIpc:     true,
				},
			},
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}

	createdAt := time.Now().UnixNano()

	metadata := &metadata.SandboxMetadata{
		ID:        sandboxStatusTestID,
		Name:      "test-name",
		Config:    config,
		CreatedAt: createdAt,
	}

	expectedStatus := &runtime.PodSandboxStatus{
		Id:        sandboxStatusTestID,
		Metadata:  config.GetMetadata(),
		CreatedAt: createdAt,
		Network:   &runtime.PodSandboxNetworkStatus{},
		Linux: &runtime.LinuxPodSandboxStatus{
			Namespaces: &runtime.Namespace{
				Options: &runtime.NamespaceOption{
					HostNetwork: true,
					HostPid:     false,
					HostIpc:     true,
				},
			},
		},
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
	}

	return metadata, expectedStatus
}

func TestToCRISandboxStatus(t *testing.T) {
	for desc, test := range map[string]struct {
		state       runtime.PodSandboxState
		expectNetNS string
	}{
		"ready sandbox should have network namespace": {
			state:       runtime.PodSandboxState_SANDBOX_READY,
			expectNetNS: "test-netns",
		},
		"not ready sandbox should not have network namespace": {
			state:       runtime.PodSandboxState_SANDBOX_NOTREADY,
			expectNetNS: "",
		},
	} {
		metadata, expect := getSandboxStatusTestData()
		metadata.NetNS = "test-netns"
		status := toCRISandboxStatus(metadata, test.state)
		expect.Linux.Namespaces.Network = test.expectNetNS
		expect.State = test.state
		assert.Equal(t, expect, status, desc)
	}
}

func TestPodSandboxStatus(t *testing.T) {
	for desc, test := range map[string]struct {
		sandboxContainers []container.Container
		injectMetadata    bool
		injectErr         error
		expectState       runtime.PodSandboxState
		expectErr         bool
		expectCalls       []string
	}{
		"sandbox status without metadata": {
			injectMetadata: false,
			expectErr:      true,
			expectCalls:    []string{},
		},
		"sandbox status with running sandbox container": {
			sandboxContainers: []container.Container{{
				ID:     sandboxStatusTestID,
				Pid:    1,
				Status: container.Status_RUNNING,
			}},
			injectMetadata: true,
			expectState:    runtime.PodSandboxState_SANDBOX_READY,
			expectCalls:    []string{"info"},
		},
		"sandbox status with stopped sandbox container": {
			sandboxContainers: []container.Container{{
				ID:     sandboxStatusTestID,
				Pid:    1,
				Status: container.Status_STOPPED,
			}},
			injectMetadata: true,
			expectState:    runtime.PodSandboxState_SANDBOX_NOTREADY,
			expectCalls:    []string{"info"},
		},
		"sandbox status with non-existing sandbox container": {
			sandboxContainers: []container.Container{},
			injectMetadata:    true,
			expectState:       runtime.PodSandboxState_SANDBOX_NOTREADY,
			expectCalls:       []string{"info"},
		},
		"sandbox status with arbitrary error": {
			sandboxContainers: []container.Container{{
				ID:     sandboxStatusTestID,
				Pid:    1,
				Status: container.Status_RUNNING,
			}},
			injectMetadata: true,
			injectErr:      errors.New("arbitrary error"),
			expectErr:      true,
			expectCalls:    []string{"info"},
		},
	} {
		t.Logf("TestCase %q", desc)
		metadata, expect := getSandboxStatusTestData()
		c := newTestCRIContainerdService()
		fake := c.containerService.(*servertesting.FakeExecutionClient)
		fake.SetFakeContainers(test.sandboxContainers)
		if test.injectMetadata {
			assert.NoError(t, c.sandboxIDIndex.Add(metadata.ID))
			assert.NoError(t, c.sandboxStore.Create(*metadata))
		}
		if test.injectErr != nil {
			fake.InjectError("info", test.injectErr)
		}
		res, err := c.PodSandboxStatus(context.Background(), &runtime.PodSandboxStatusRequest{
			PodSandboxId: sandboxStatusTestID,
		})
		assert.Equal(t, test.expectCalls, fake.GetCalledNames())
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, res)
			continue
		}
		assert.NoError(t, err)
		require.NotNil(t, res)
		expect.State = test.expectState
		assert.Equal(t, expect, res.GetStatus())
	}
}
