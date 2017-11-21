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

package integration

import (
	"testing"

	"github.com/containerd/containerd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

func TestSandboxCleanRemove(t *testing.T) {
	ctx := context.Background()
	t.Logf("Create a sandbox")
	sbConfig := PodSandboxConfig("sandbox", "clean-remove")
	sb, err := runtimeService.RunPodSandbox(sbConfig)
	require.NoError(t, err)
	defer func() {
		// Make sure the sandbox is cleaned up in any case.
		runtimeService.StopPodSandbox(sb)
		runtimeService.RemovePodSandbox(sb)
	}()

	t.Logf("Should not be able to remove the sandbox when it's still running")
	assert.Error(t, runtimeService.RemovePodSandbox(sb))

	t.Logf("Delete sandbox task from containerd")
	cntr, err := containerdClient.LoadContainer(ctx, sb)
	require.NoError(t, err)
	task, err := cntr.Task(ctx, nil)
	require.NoError(t, err)
	_, err = task.Delete(ctx, containerd.WithProcessKill)
	require.NoError(t, err)

	t.Logf("Sandbox state should be NOTREADY")
	status, err := runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	assert.Equal(t, runtime.PodSandboxState_SANDBOX_NOTREADY, status.GetState())

	t.Logf("Should not be able to remove the sandbox when netns is not closed")
	assert.Error(t, runtimeService.RemovePodSandbox(sb))

	t.Logf("Should be able to remove the sandbox after properly stopped")
	assert.NoError(t, runtimeService.StopPodSandbox(sb))
	assert.NoError(t, runtimeService.RemovePodSandbox(sb))
}
