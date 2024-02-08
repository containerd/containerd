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

package podsandbox

import (
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/annotations"
	"github.com/containerd/containerd/v2/internal/cri/opts"
)

func getRunPodSandboxTestData() (*runtime.PodSandboxConfig, *imagespec.ImageConfig, func(*testing.T, string, *runtimespec.Spec)) {
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-name",
			Uid:       "test-uid",
			Namespace: "test-ns",
			Attempt:   1,
		},
		Hostname:     "test-hostname",
		LogDirectory: "test-log-directory",
		Labels:       map[string]string{"a": "b"},
		Annotations:  map[string]string{"c": "d"},
		Windows: &runtime.WindowsPodSandboxConfig{
			SecurityContext: &runtime.WindowsSandboxSecurityContext{
				RunAsUsername:  "test-user",
				CredentialSpec: "{\"test\": \"spec\"}",
				HostProcess:    false,
			},
		},
	}
	imageConfig := &imagespec.ImageConfig{
		Env:        []string{"a=b", "c=d"},
		Entrypoint: []string{"/pause"},
		Cmd:        []string{"forever"},
		WorkingDir: "/workspace",
		User:       "test-image-user",
	}
	specCheck := func(t *testing.T, id string, spec *runtimespec.Spec) {
		assert.Equal(t, "test-hostname", spec.Hostname)
		assert.Nil(t, spec.Root)
		assert.Contains(t, spec.Process.Env, "a=b", "c=d")
		assert.Equal(t, []string{"/pause", "forever"}, spec.Process.Args)
		assert.Equal(t, "/workspace", spec.Process.Cwd)
		assert.EqualValues(t, *spec.Windows.Resources.CPU.Shares, opts.DefaultSandboxCPUshares)

		// Also checks if override of the image configs user is behaving.
		t.Logf("Check username")
		assert.Contains(t, spec.Process.User.Username, "test-user")

		t.Logf("Check credential spec")
		assert.Contains(t, spec.Windows.CredentialSpec, "{\"test\": \"spec\"}")

		t.Logf("Check PodSandbox annotations")
		assert.Contains(t, spec.Annotations, annotations.SandboxID)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxID], id)

		assert.Contains(t, spec.Annotations, annotations.ContainerType)
		assert.EqualValues(t, spec.Annotations[annotations.ContainerType], annotations.ContainerTypeSandbox)

		assert.Contains(t, spec.Annotations, annotations.SandboxNamespace)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxNamespace], "test-ns")

		assert.Contains(t, spec.Annotations, annotations.SandboxUID)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxUID], "test-uid")

		assert.Contains(t, spec.Annotations, annotations.SandboxName)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxName], "test-name")

		assert.Contains(t, spec.Annotations, annotations.SandboxLogDir)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxLogDir], "test-log-directory")

		assert.Contains(t, spec.Annotations, annotations.WindowsHostProcess)
		assert.EqualValues(t, spec.Annotations[annotations.WindowsHostProcess], "false")
	}
	return config, imageConfig, specCheck
}

func TestSandboxWindowsNetworkNamespace(t *testing.T) {
	testID := "test-id"
	nsPath := "test-cni"
	c := newControllerService()

	config, imageConfig, specCheck := getRunPodSandboxTestData()
	spec, err := c.sandboxContainerSpec(testID, config, imageConfig, nsPath, nil)
	assert.NoError(t, err)
	assert.NotNil(t, spec)
	specCheck(t, testID, spec)
	assert.NotNil(t, spec.Windows)
	assert.NotNil(t, spec.Windows.Network)
	assert.Equal(t, nsPath, spec.Windows.Network.NetworkNamespace)
}
