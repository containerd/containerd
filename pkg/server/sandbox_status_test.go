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

	"github.com/containerd/containerd/api/types/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
)

// Variables used in the following test.

const (
	sandboxStatusTestID    = "test-id"
	sandboxStatusTestIP    = "10.10.10.10"
	sandboxStatusTestNetNS = "test-netns"
)

func getSandboxStatusTestData() (*sandboxstore.Sandbox, *runtime.PodSandboxStatus) {
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

	sandbox := &sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{
			ID:        sandboxStatusTestID,
			Name:      "test-name",
			Config:    config,
			CreatedAt: createdAt,
			NetNS:     sandboxStatusTestNetNS,
		},
	}

	expectedStatus := &runtime.PodSandboxStatus{
		Id:        sandboxStatusTestID,
		Metadata:  config.GetMetadata(),
		CreatedAt: createdAt,
		Network:   &runtime.PodSandboxNetworkStatus{Ip: ""},
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

	return sandbox, expectedStatus
}

func TestPodSandboxStatus(t *testing.T) {
	for desc, test := range map[string]struct {
		sandboxTasks     []task.Task
		injectSandbox    bool
		injectErr        error
		injectIP         bool
		injectCNIErr     error
		expectState      runtime.PodSandboxState
		expectErr        bool
		expectCalls      []string
		expectedCNICalls []string
	}{
		"sandbox status without metadata": {
			injectSandbox:    false,
			expectErr:        true,
			expectCalls:      []string{},
			expectedCNICalls: []string{},
		},
		"sandbox status with running sandbox container": {
			sandboxTasks: []task.Task{{
				ID:     sandboxStatusTestID,
				Pid:    1,
				Status: task.StatusRunning,
			}},
			injectSandbox:    true,
			expectState:      runtime.PodSandboxState_SANDBOX_READY,
			expectCalls:      []string{"info"},
			expectedCNICalls: []string{"GetContainerNetworkStatus"},
		},
		"sandbox status with stopped sandbox container": {
			sandboxTasks: []task.Task{{
				ID:     sandboxStatusTestID,
				Pid:    1,
				Status: task.StatusStopped,
			}},
			injectSandbox:    true,
			expectState:      runtime.PodSandboxState_SANDBOX_NOTREADY,
			expectCalls:      []string{"info"},
			expectedCNICalls: []string{"GetContainerNetworkStatus"},
		},
		"sandbox status with non-existing sandbox container": {
			sandboxTasks:     []task.Task{},
			injectSandbox:    true,
			expectState:      runtime.PodSandboxState_SANDBOX_NOTREADY,
			expectCalls:      []string{"info"},
			expectedCNICalls: []string{"GetContainerNetworkStatus"},
		},
		"sandbox status with arbitrary error": {
			sandboxTasks: []task.Task{{
				ID:     sandboxStatusTestID,
				Pid:    1,
				Status: task.StatusRunning,
			}},
			injectSandbox:    true,
			expectState:      runtime.PodSandboxState_SANDBOX_READY,
			injectErr:        errors.New("arbitrary error"),
			expectErr:        true,
			expectCalls:      []string{"info"},
			expectedCNICalls: []string{},
		},
		"sandbox status with IP address": {
			sandboxTasks: []task.Task{{
				ID:     sandboxStatusTestID,
				Pid:    1,
				Status: task.StatusRunning,
			}},
			injectSandbox:    true,
			expectState:      runtime.PodSandboxState_SANDBOX_READY,
			expectCalls:      []string{"info"},
			injectIP:         true,
			expectedCNICalls: []string{"GetContainerNetworkStatus"},
		},
		"sandbox status with GetContainerNetworkStatus returns error": {
			sandboxTasks: []task.Task{{
				ID:     sandboxStatusTestID,
				Pid:    1,
				Status: task.StatusRunning,
			}},
			injectSandbox:    true,
			expectState:      runtime.PodSandboxState_SANDBOX_READY,
			expectCalls:      []string{"info"},
			expectedCNICalls: []string{"GetContainerNetworkStatus"},
			injectCNIErr:     errors.New("get container network status error"),
		},
	} {
		t.Logf("TestCase %q", desc)
		sandbox, expect := getSandboxStatusTestData()
		expect.Network.Ip = ""
		c := newTestCRIContainerdService()
		fake := c.taskService.(*servertesting.FakeExecutionClient)
		fakeCNIPlugin := c.netPlugin.(*servertesting.FakeCNIPlugin)
		fake.SetFakeTasks(test.sandboxTasks)
		if test.injectSandbox {
			assert.NoError(t, c.sandboxStore.Add(*sandbox))
		}
		if test.injectErr != nil {
			fake.InjectError("info", test.injectErr)
		}
		if test.injectCNIErr != nil {
			fakeCNIPlugin.InjectError("GetContainerNetworkStatus", test.injectCNIErr)
		}
		if test.injectIP {
			fakeCNIPlugin.SetFakePodNetwork(sandbox.NetNS, sandbox.Config.GetMetadata().GetNamespace(),
				sandbox.Config.GetMetadata().GetName(), sandboxStatusTestID, sandboxStatusTestIP)
			expect.Network.Ip = sandboxStatusTestIP
		}
		res, err := c.PodSandboxStatus(context.Background(), &runtime.PodSandboxStatusRequest{
			PodSandboxId: sandboxStatusTestID,
		})
		assert.Equal(t, test.expectCalls, fake.GetCalledNames())
		assert.Equal(t, test.expectedCNICalls, fakeCNIPlugin.GetCalledNames())
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
