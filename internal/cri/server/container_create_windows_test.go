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

package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/annotations"
	"github.com/containerd/containerd/v2/internal/cri/config"
)

func getSandboxConfig() *runtime.PodSandboxConfig {
	return &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-sandbox-name",
			Uid:       "test-sandbox-uid",
			Namespace: "test-sandbox-ns",
			Attempt:   2,
		},
		Windows:     &runtime.WindowsPodSandboxConfig{},
		Hostname:    "test-hostname",
		Annotations: map[string]string{"c": "d"},
	}
}

func getCreateContainerTestData() (*runtime.ContainerConfig, *runtime.PodSandboxConfig,
	*imagespec.ImageConfig, func(*testing.T, string, string, uint32, *runtimespec.Spec)) {
	config := &runtime.ContainerConfig{
		Metadata: &runtime.ContainerMetadata{
			Name:    "test-name",
			Attempt: 1,
		},
		Image: &runtime.ImageSpec{
			Image: "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113799",
		},
		Command:    []string{"test", "command"},
		Args:       []string{"test", "args"},
		WorkingDir: "test-cwd",
		Envs: []*runtime.KeyValue{
			{Key: "k1", Value: "v1"},
			{Key: "k2", Value: "v2"},
			{Key: "k3", Value: "v3=v3bis"},
			{Key: "k4", Value: "v4=v4bis=foop"},
		},
		Mounts: []*runtime.Mount{
			// everything default
			{
				ContainerPath: "container-path-1",
				HostPath:      "host-path-1",
			},
			// readOnly
			{
				ContainerPath: "container-path-2",
				HostPath:      "host-path-2",
				Readonly:      true,
			},
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
		Windows: &runtime.WindowsContainerConfig{
			Resources: &runtime.WindowsContainerResources{
				CpuShares:          100,
				CpuCount:           200,
				CpuMaximum:         300,
				MemoryLimitInBytes: 400,
			},
			SecurityContext: &runtime.WindowsContainerSecurityContext{
				RunAsUsername:  "test-user",
				CredentialSpec: "{\"test\": \"spec\"}",
				HostProcess:    false,
			},
		},
	}
	sandboxConfig := getSandboxConfig()
	imageConfig := &imagespec.ImageConfig{
		Env:        []string{"ik1=iv1", "ik2=iv2", "ik3=iv3=iv3bis", "ik4=iv4=iv4bis=boop"},
		Entrypoint: []string{"/entrypoint"},
		Cmd:        []string{"cmd"},
		WorkingDir: "/workspace",
		User:       "ContainerUser",
	}
	specCheck := func(t *testing.T, id string, sandboxID string, sandboxPid uint32, spec *runtimespec.Spec) {
		assert.Nil(t, spec.Root)
		assert.Equal(t, "test-hostname", spec.Hostname)
		assert.Equal(t, []string{"test", "command", "test", "args"}, spec.Process.Args)
		assert.Equal(t, "test-cwd", spec.Process.Cwd)
		assert.Contains(t, spec.Process.Env, "k1=v1", "k2=v2", "k3=v3=v3bis", "ik4=iv4=iv4bis=boop")
		assert.Contains(t, spec.Process.Env, "ik1=iv1", "ik2=iv2", "ik3=iv3=iv3bis", "k4=v4=v4bis=foop")

		t.Logf("Check bind mount")
		checkMount(t, spec.Mounts, "host-path-1", "container-path-1", "", []string{"rw"}, nil)
		checkMount(t, spec.Mounts, "host-path-2", "container-path-2", "", []string{"ro"}, nil)

		t.Logf("Check resource limits")
		assert.EqualValues(t, *spec.Windows.Resources.CPU.Shares, 100)
		assert.EqualValues(t, *spec.Windows.Resources.CPU.Count, 200)
		assert.EqualValues(t, *spec.Windows.Resources.CPU.Maximum, 300)
		assert.EqualValues(t, *spec.Windows.Resources.CPU.Maximum, 300)
		assert.EqualValues(t, *spec.Windows.Resources.Memory.Limit, 400)

		// Also checks if override of the image configs user is behaving.
		t.Logf("Check username")
		assert.Contains(t, spec.Process.User.Username, "test-user")

		t.Logf("Check credential spec")
		assert.Contains(t, spec.Windows.CredentialSpec, "{\"test\": \"spec\"}")

		t.Logf("Check PodSandbox annotations")
		assert.Contains(t, spec.Annotations, annotations.SandboxID)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxID], sandboxID)

		assert.Contains(t, spec.Annotations, annotations.ContainerType)
		assert.EqualValues(t, spec.Annotations[annotations.ContainerType], annotations.ContainerTypeContainer)

		assert.Contains(t, spec.Annotations, annotations.SandboxNamespace)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxNamespace], "test-sandbox-ns")

		assert.Contains(t, spec.Annotations, annotations.SandboxUID)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxUID], "test-sandbox-uid")

		assert.Contains(t, spec.Annotations, annotations.SandboxName)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxName], "test-sandbox-name")

		assert.Contains(t, spec.Annotations, annotations.WindowsHostProcess)
		assert.EqualValues(t, spec.Annotations[annotations.WindowsHostProcess], "false")
	}
	return config, sandboxConfig, imageConfig, specCheck
}

func TestContainerWindowsNetworkNamespace(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	nsPath := "test-cni"
	c := newTestCRIService()

	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, nsPath, testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, config.Runtime{}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, spec)
	specCheck(t, testID, testSandboxID, testPid, spec)
	assert.NotNil(t, spec.Windows)
	assert.NotNil(t, spec.Windows.Network)
	assert.Equal(t, nsPath, spec.Windows.Network.NetworkNamespace)
}

func TestMountCleanPath(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	nsPath := "test-cni"
	c := newTestCRIService()

	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	containerConfig.Mounts = append(containerConfig.Mounts, &runtime.Mount{
		ContainerPath: "c:/test/container-path",
		HostPath:      "c:/test/host-path",
	})
	spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, nsPath, testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, config.Runtime{}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, spec)
	specCheck(t, testID, testSandboxID, testPid, spec)
	checkMount(t, spec.Mounts, "c:\\test\\host-path", "c:\\test\\container-path", "", []string{"rw"}, nil)
}

func TestMountNamedPipe(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	nsPath := "test-cni"
	c := newTestCRIService()

	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	containerConfig.Mounts = append(containerConfig.Mounts, &runtime.Mount{
		ContainerPath: `\\.\pipe\foo`,
		HostPath:      `\\.\pipe\foo`,
	})
	spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, nsPath, testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, config.Runtime{}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, spec)
	specCheck(t, testID, testSandboxID, testPid, spec)
	checkMount(t, spec.Mounts, `\\.\pipe\foo`, `\\.\pipe\foo`, "", []string{"rw"}, nil)
}

func TestHostProcessRequirements(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	for _, test := range []struct {
		desc                 string
		containerHostProcess bool
		sandboxHostProcess   bool
		expectError          bool
	}{
		{
			desc:                 "hostprocess container in non-hostprocess sandbox should fail",
			containerHostProcess: true,
			sandboxHostProcess:   false,
			expectError:          true,
		},
		{
			desc:                 "hostprocess container in hostprocess sandbox should be fine",
			containerHostProcess: true,
			sandboxHostProcess:   true,
			expectError:          false,
		},
		{
			desc:                 "non-hostprocess container in hostprocess sandbox should fail",
			containerHostProcess: false,
			sandboxHostProcess:   true,
			expectError:          true,
		},
		{
			desc:                 "non-hostprocess container in non-hostprocess sandbox should be fine",
			containerHostProcess: false,
			sandboxHostProcess:   false,
			expectError:          false,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			containerConfig.Windows.SecurityContext.HostProcess = test.containerHostProcess
			sandboxConfig.Windows.SecurityContext = &runtime.WindowsSandboxSecurityContext{
				HostProcess: test.sandboxHostProcess,
			}
			_, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime, nil)
			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEntrypointAndCmdForArgsEscaped(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	nsPath := "test-ns"
	c := newTestCRIService()

	for name, test := range map[string]struct {
		imgEntrypoint       []string
		imgCmd              []string
		command             []string
		args                []string
		expectedArgs        []string
		expectedCommandLine string
		ArgsEscaped         bool
	}{
		// override image entrypoint and cmd in shell form with container args and verify expected runtime spec
		"TestShellFormImgEntrypointCmdWithCtrArgs": {
			imgEntrypoint:       []string{`"C:\My Folder\MyProcess.exe" -arg1 "test value"`},
			imgCmd:              []string{`cmd -args "hello world"`},
			command:             nil,
			args:                []string{`cmd -args "additional args"`},
			expectedArgs:        nil,
			expectedCommandLine: `"C:\My Folder\MyProcess.exe" -arg1 "test value" "cmd -args \"additional args\""`,
			ArgsEscaped:         true,
		},
		// check image entrypoint and cmd in shell form without overriding with container command and args and verify expected runtime spec
		"TestShellFormImgEntrypointCmdWithoutCtrArgs": {
			imgEntrypoint:       []string{`"C:\My Folder\MyProcess.exe" -arg1 "test value"`},
			imgCmd:              []string{`cmd -args "hello world"`},
			command:             nil,
			args:                nil,
			expectedArgs:        nil,
			expectedCommandLine: `"C:\My Folder\MyProcess.exe" -arg1 "test value" "cmd -args \"hello world\""`,
			ArgsEscaped:         true,
		},
		// override image entrypoint and cmd by container command and args in shell form and verify expected runtime spec
		"TestShellFormImgEntrypointCmdWithCtrEntrypointAndArgs": {
			imgEntrypoint:       []string{`"C:\My Folder\MyProcess.exe" -arg1 "test value"`},
			imgCmd:              []string{`cmd -args "hello world"`},
			command:             []string{`C:\My Folder\MyProcess.exe`, "-arg1", "additional test value"},
			args:                []string{"cmd", "-args", "additional args"},
			expectedArgs:        nil,
			expectedCommandLine: `"C:\My Folder\MyProcess.exe" -arg1 "additional test value" cmd -args "additional args"`,
			ArgsEscaped:         true,
		},
		// override image cmd by container args in exec form and verify expected runtime spec
		"TestExecFormImgEntrypointCmdWithCtrArgs": {
			imgEntrypoint:       []string{`C:\My Folder\MyProcess.exe`, "-arg1", "test value"},
			imgCmd:              []string{"cmd", "-args", "hello world"},
			command:             nil,
			args:                []string{"additional", "args"},
			expectedArgs:        []string{`C:\My Folder\MyProcess.exe`, "-arg1", "test value", "additional", "args"},
			expectedCommandLine: "",
			ArgsEscaped:         false,
		},
		// check image entrypoint and cmd in exec form without overriding with container command and args and verify expected runtime spec
		"TestExecFormImgEntrypointCmdWithoutCtrArgs": {
			imgEntrypoint:       []string{`C:\My Folder\MyProcess.exe`, "-arg1", "test value"},
			imgCmd:              []string{"cmd", "-args", "hello world"},
			command:             nil,
			args:                nil,
			expectedArgs:        []string{`C:\My Folder\MyProcess.exe`, "-arg1", "test value", "cmd", "-args", "hello world"},
			expectedCommandLine: "",
			ArgsEscaped:         false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			imageConfig := &imagespec.ImageConfig{
				Entrypoint:  test.imgEntrypoint,
				Cmd:         test.imgCmd,
				ArgsEscaped: test.ArgsEscaped,
			}
			sandboxConfig := getSandboxConfig()
			containerConfig := &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name:    "test-name",
					Attempt: 1,
				},
				Image: &runtime.ImageSpec{
					Image: testImageName,
				},
				Command: test.command,
				Args:    test.args,
				Windows: &runtime.WindowsContainerConfig{},
			}
			runtimeSpec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, nsPath, testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, config.Runtime{}, nil)
			assert.NoError(t, err)
			assert.NotNil(t, runtimeSpec)

			// check the runtime spec for expected commandline and args
			actualCommandLine := runtimeSpec.Process.CommandLine
			actualArgs := runtimeSpec.Process.Args

			require.Equal(t, actualArgs, test.expectedArgs)
			require.Equal(t, actualCommandLine, test.expectedCommandLine)
		})
	}
}
