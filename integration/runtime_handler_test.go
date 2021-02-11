// +build linux

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

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func TestRuntimeHandler(t *testing.T) {

	// Make sure the test runs with a sufficent flag
	localHandler := "foo"
	if *runtimeHandler != "" {
		localHandler = *runtimeHandler
	}

	t.Logf("Create a sandbox")
	sbConfig := PodSandboxConfig("sandbox", "test-runtime-handler")
	t.Logf("the --runtime-handler flag value is: %s", localHandler)
	sb, err := runtimeService.RunPodSandbox(sbConfig, localHandler)
	require.NoError(t, err)
	defer func() {
		// Make sure the sandbox is cleaned up in any case.
		runtimeService.StopPodSandbox(sb)
		runtimeService.RemovePodSandbox(sb)
	}()

	t.Logf("Verify runtimeService.PodSandboxStatus sets RuntimeHandler")
	sbStatus, err := runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	t.Logf("runtimeService.PodSandboxStatus sets RuntimeHandler to %s", sbStatus.RuntimeHandler)
	assert.Equal(t, localHandler, sbStatus.RuntimeHandler)

	t.Logf("Verify runtimeService.ListPodSandbox sets RuntimeHandler")
	sandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	t.Logf("runtimeService.ListPodSandbox sets RuntimeHandler to %s", sbStatus.RuntimeHandler)
	assert.Equal(t, localHandler, sandboxes[0].RuntimeHandler)
}
