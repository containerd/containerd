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

	"github.com/stretchr/testify/require"
)

func TestPodSandboxController_ShouldBackoffExitEventWhenFail(t *testing.T) {
	t.Logf("Inject Shim failpoint")

	sbConfig := PodSandboxConfig(t.Name(), "failpoint")
	injectShimFailpoint(t, sbConfig, map[string]string{
		"Delete": "1*error(retry)",
	})

	t.Log("Create a sandbox")
	sbID, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
	require.NoError(t, err)

	t.Log("Stop the sandbox")
	err = runtimeService.StopPodSandbox(sbID)
	require.NoError(t, err)

	t.Log("Delete sandbox")
	err = runtimeService.RemovePodSandbox(sbID)
	require.NoError(t, err)
}
