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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func TestRuntimes(t *testing.T) {
	runtimes := []string{*runtimeHandler, "runc"}
	for idx, rt := range runtimes {
		t.Logf("Create a sandbox")
		sbConfig := PodSandboxConfig(fmt.Sprintf("sandbox-%d", idx), fmt.Sprintf("test-runtime-handler-%d", idx))
		if rt == "" {
			t.Logf("The --runtime-handler flag value is empty which results internally to setting the default runtime")
		} else {
			t.Logf("The --runtime-handler flag value is %s", rt)
		}
		sb, err := runtimeService.RunPodSandbox(sbConfig, rt)
		require.NoError(t, err)
		defer func() {
			// Make sure the sandbox is cleaned up in any case.
			runtimeService.StopPodSandbox(sb)
			runtimeService.RemovePodSandbox(sb)
		}()

		t.Logf("Verify runtimeService.PodSandboxStatus() returns previously set runtimeHandler")
		sbStatus, err := runtimeService.PodSandboxStatus(sb)
		require.NoError(t, err)
		assert.Equal(t, rt, sbStatus.RuntimeHandler)

		t.Logf("Verify runtimeService.ListPodSandbox() returns previously set runtimeHandler")
		sandboxes, err := runtimeService.ListPodSandbox(&runtime.PodSandboxFilter{})
		require.NoError(t, err)
		assert.Equal(t, rt, sandboxes[idx].RuntimeHandler)
	}
}
