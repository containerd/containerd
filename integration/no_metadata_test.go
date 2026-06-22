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
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestRunPodSandboxWithoutMetadata(t *testing.T) {
	sbConfig := &runtime.PodSandboxConfig{}
	_, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.Error(t, err)
	_, err = runtimeService.Status()
	require.NoError(t, err)
}

func TestCreateContainerWithoutMetadata(t *testing.T) {
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-create")
	config := &runtime.ContainerConfig{}
	_, err := runtimeService.CreateContainer(sb, config, sbConfig)
	require.Error(t, err)
	_, err = runtimeService.Status()
	require.NoError(t, err)
}
