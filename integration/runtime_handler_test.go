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
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// TODO(chrisfegly): add/update test(s) to allow testing of multiple runtimes at the same time
func TestRuntimeHandler(t *testing.T) {
	t.Logf("Create a sandbox")
	if *runtimeHandler == "" {
		t.Logf("The --runtime-handler flag value is empty which results internally to setting the default runtime")
	} else {
		t.Logf("The --runtime-handler flag value is %s", *runtimeHandler)
	}
	sb, _ := PodSandboxConfigWithCleanup(t, "sandbox", "test-runtime-handler")

	t.Logf("Verify runtimeService.PodSandboxStatus() returns previously set runtimeHandler")
	sbStatus, err := runtimeService.PodSandboxStatus(sb)
	require.NoError(t, err)
	assert.Equal(t, *runtimeHandler, sbStatus.RuntimeHandler)

	t.Logf("Verify runtimeService.ListPodSandbox() returns previously set runtimeHandler")
	sandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
	require.NoError(t, err)
	assert.Equal(t, *runtimeHandler, sandboxes[0].RuntimeHandler)
}
