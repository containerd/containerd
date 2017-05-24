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
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/container"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func getStartContainerTestData() (*runtime.ContainerConfig, *runtime.PodSandboxConfig,
	func(*testing.T, string, uint32, *runtimespec.Spec)) {
	config := &runtime.ContainerConfig{
		Metadata: &runtime.ContainerMetadata{
			Name:    "test-name",
			Attempt: 1,
		},
		Command:    []string{"test", "command"},
		Args:       []string{"test", "args"},
		WorkingDir: "test-cwd",
		Envs: []*runtime.KeyValue{
			{Key: "k1", Value: "v1"},
			{Key: "k2", Value: "v2"},
		},
		Mounts: []*runtime.Mount{
			{
				ContainerPath: "container-path-1",
				HostPath:      "host-path-1",
			},
			{
				ContainerPath: "container-path-2",
				HostPath:      "host-path-2",
				Readonly:      true,
			},
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
		Linux: &runtime.LinuxContainerConfig{
			Resources: &runtime.LinuxContainerResources{
				CpuPeriod:          100,
				CpuQuota:           200,
				CpuShares:          300,
				MemoryLimitInBytes: 400,
				OomScoreAdj:        500,
			},
			SecurityContext: &runtime.LinuxContainerSecurityContext{
				Capabilities: &runtime.Capability{
					AddCapabilities:  []string{"CAP_SYS_ADMIN"},
					DropCapabilities: []string{"CAP_CHOWN"},
				},
				SupplementalGroups: []int64{1111, 2222},
			},
		},
	}
	sandboxConfig := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-sandbox-name",
			Uid:       "test-sandbox-uid",
			Namespace: "test-sandbox-ns",
			Attempt:   2,
		},
		Linux: &runtime.LinuxPodSandboxConfig{
			CgroupParent: "/test/cgroup/parent",
		},
	}
	specCheck := func(t *testing.T, id string, sandboxPid uint32, spec *runtimespec.Spec) {
		assert.Equal(t, relativeRootfsPath, spec.Root.Path)
		assert.Equal(t, []string{"test", "command", "test", "args"}, spec.Process.Args)
		assert.Equal(t, "test-cwd", spec.Process.Cwd)
		assert.Contains(t, spec.Process.Env, "k1=v1", "k2=v2")

		t.Logf("Check bind mount")
		found1, found2 := false, false
		for _, m := range spec.Mounts {
			if m.Source == "host-path-1" {
				assert.Equal(t, m.Destination, "container-path-1")
				assert.Contains(t, m.Options, "rw")
				found1 = true
			}
			if m.Source == "host-path-2" {
				assert.Equal(t, m.Destination, "container-path-2")
				assert.Contains(t, m.Options, "ro")
				found2 = true
			}
		}
		assert.True(t, found1)
		assert.True(t, found2)

		t.Logf("Check resource limits")
		assert.EqualValues(t, *spec.Linux.Resources.CPU.Period, 100)
		assert.EqualValues(t, *spec.Linux.Resources.CPU.Quota, 200)
		assert.EqualValues(t, *spec.Linux.Resources.CPU.Shares, 300)
		assert.EqualValues(t, *spec.Linux.Resources.Memory.Limit, 400)
		assert.EqualValues(t, *spec.Linux.Resources.OOMScoreAdj, 500)

		t.Logf("Check capabilities")
		assert.Contains(t, spec.Process.Capabilities.Bounding, "CAP_SYS_ADMIN")
		assert.Contains(t, spec.Process.Capabilities.Effective, "CAP_SYS_ADMIN")
		assert.Contains(t, spec.Process.Capabilities.Inheritable, "CAP_SYS_ADMIN")
		assert.Contains(t, spec.Process.Capabilities.Permitted, "CAP_SYS_ADMIN")
		assert.Contains(t, spec.Process.Capabilities.Ambient, "CAP_SYS_ADMIN")
		assert.NotContains(t, spec.Process.Capabilities.Bounding, "CAP_CHOWN")
		assert.NotContains(t, spec.Process.Capabilities.Effective, "CAP_CHOWN")
		assert.NotContains(t, spec.Process.Capabilities.Inheritable, "CAP_CHOWN")
		assert.NotContains(t, spec.Process.Capabilities.Permitted, "CAP_CHOWN")
		assert.NotContains(t, spec.Process.Capabilities.Ambient, "CAP_CHOWN")

		t.Logf("Check supplemental groups")
		assert.Contains(t, spec.Process.User.AdditionalGids, uint32(1111))
		assert.Contains(t, spec.Process.User.AdditionalGids, uint32(2222))

		t.Logf("Check cgroup path")
		assert.Equal(t, getCgroupsPath("/test/cgroup/parent", id), spec.Linux.CgroupsPath)

		t.Logf("Check namespaces")
		assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: runtimespec.NetworkNamespace,
			Path: getNetworkNamespace(sandboxPid),
		})
		assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: runtimespec.IPCNamespace,
			Path: getIPCNamespace(sandboxPid),
		})
		assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: runtimespec.UTSNamespace,
			Path: getUTSNamespace(sandboxPid),
		})
		assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: runtimespec.PIDNamespace,
			Path: getPIDNamespace(sandboxPid),
		})
	}
	return config, sandboxConfig, specCheck
}

func TestGeneralContainerSpec(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, specCheck := getStartContainerTestData()
	c := newTestCRIContainerdService()
	spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig)
	assert.NoError(t, err)
	specCheck(t, testID, testPid, spec)
}

func TestContainerSpecTty(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, specCheck := getStartContainerTestData()
	c := newTestCRIContainerdService()
	for _, tty := range []bool{true, false} {
		config.Tty = tty
		spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig)
		assert.NoError(t, err)
		specCheck(t, testID, testPid, spec)
		assert.Equal(t, tty, spec.Process.Terminal)
	}
}

func TestContainerSpecReadonlyRootfs(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, specCheck := getStartContainerTestData()
	c := newTestCRIContainerdService()
	for _, readonly := range []bool{true, false} {
		config.Linux.SecurityContext.ReadonlyRootfs = readonly
		spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig)
		assert.NoError(t, err)
		specCheck(t, testID, testPid, spec)
		assert.Equal(t, readonly, spec.Root.Readonly)
	}
}

func TestStartContainer(t *testing.T) {
	testID := "test-id"
	testSandboxID := "test-sandbox-id"
	testSandboxPid := uint32(4321)
	config, sandboxConfig, specCheck := getStartContainerTestData()
	testMetadata := &metadata.ContainerMetadata{
		ID:        testID,
		Name:      "test-name",
		SandboxID: testSandboxID,
		Config:    config,
		CreatedAt: time.Now().UnixNano(),
	}
	testSandboxMetadata := &metadata.SandboxMetadata{
		ID:     testSandboxID,
		Name:   "test-sandbox-name",
		Config: sandboxConfig,
	}
	testSandboxContainer := &container.Container{
		ID:     testSandboxID,
		Pid:    testSandboxPid,
		Status: container.Status_RUNNING,
	}
	for desc, test := range map[string]struct {
		containerMetadata          *metadata.ContainerMetadata
		sandboxMetadata            *metadata.SandboxMetadata
		sandboxContainerdContainer *container.Container
		prepareFIFOErr             error
		createContainerErr         error
		startContainerErr          error
		expectStateChange          bool
		expectCalls                []string
		expectErr                  bool
	}{
		"should return error when container does not exist": {
			containerMetadata:          nil,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when container is not in created state": {
			containerMetadata: &metadata.ContainerMetadata{
				ID:        testID,
				Name:      "test-name",
				SandboxID: testSandboxID,
				Config:    config,
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when container is in removing state": {
			containerMetadata: &metadata.ContainerMetadata{
				ID:        testID,
				Name:      "test-name",
				SandboxID: testSandboxID,
				Config:    config,
				CreatedAt: time.Now().UnixNano(),
				Removing:  true,
			},
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when sandbox does not exist": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            nil,
			sandboxContainerdContainer: testSandboxContainer,
			expectStateChange:          true,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when sandbox is not running": {
			containerMetadata: testMetadata,
			sandboxMetadata:   testSandboxMetadata,
			sandboxContainerdContainer: &container.Container{
				ID:     testSandboxID,
				Pid:    testSandboxPid,
				Status: container.Status_STOPPED,
			},
			expectStateChange: true,
			expectCalls:       []string{"info"},
			expectErr:         true,
		},
		"should return error when fail to open streaming pipes": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			prepareFIFOErr:             errors.New("open error"),
			expectStateChange:          true,
			expectCalls:                []string{"info"},
			expectErr:                  true,
		},
		"should return error when fail to create container": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			createContainerErr:         errors.New("create error"),
			expectStateChange:          true,
			expectCalls:                []string{"info", "create"},
			expectErr:                  true,
		},
		"should return error when fail to start container": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			startContainerErr:          errors.New("start error"),
			expectStateChange:          true,
			// cleanup the containerd container.
			expectCalls: []string{"info", "create", "start", "delete"},
			expectErr:   true,
		},
		"should be able to start container successfully": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			expectStateChange:          true,
			expectCalls:                []string{"info", "create", "start"},
			expectErr:                  false,
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := c.containerService.(*servertesting.FakeExecutionClient)
		fakeOS := c.os.(*ostesting.FakeOS)
		if test.containerMetadata != nil {
			assert.NoError(t, c.containerStore.Create(*test.containerMetadata))
		}
		if test.sandboxMetadata != nil {
			assert.NoError(t, c.sandboxStore.Create(*test.sandboxMetadata))
		}
		if test.sandboxContainerdContainer != nil {
			fake.SetFakeContainers([]container.Container{*test.sandboxContainerdContainer})
		}
		// TODO(random-liu): Test behavior with different streaming config.
		fakeOS.OpenFifoFn = func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
			return nopReadWriteCloser{}, test.prepareFIFOErr
		}
		if test.createContainerErr != nil {
			fake.InjectError("create", test.createContainerErr)
		}
		if test.startContainerErr != nil {
			fake.InjectError("start", test.startContainerErr)
		}
		resp, err := c.StartContainer(context.Background(), &runtime.StartContainerRequest{
			ContainerId: testID,
		})
		// Check containerd functions called.
		assert.Equal(t, test.expectCalls, fake.GetCalledNames())
		// Check results returned.
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, resp)
		} else {
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		}
		// Check container state.
		meta, err := c.containerStore.Get(testID)
		if !test.expectStateChange {
			// Do not check the error, because container may not exist
			// in the test case.
			assert.Equal(t, meta, test.containerMetadata)
			continue
		}
		assert.NoError(t, err)
		require.NotNil(t, meta)
		if test.expectErr {
			t.Logf("container state should be in exited state when fail to start")
			assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, meta.State())
			assert.Zero(t, meta.Pid)
			assert.EqualValues(t, errorStartExitCode, meta.ExitCode)
			assert.Equal(t, errorStartReason, meta.Reason)
			assert.NotEmpty(t, meta.Message)
			_, err := fake.Info(context.Background(), &execution.InfoRequest{ID: testID})
			assert.True(t, isContainerdContainerNotExistError(err),
				"containerd container should be cleaned up after when fail to start")
			continue
		}
		t.Logf("container state should be running when start successfully")
		assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, meta.State())
		info, err := fake.Info(context.Background(), &execution.InfoRequest{ID: testID})
		assert.NoError(t, err)
		pid := info.Pid
		assert.Equal(t, pid, meta.Pid)
		assert.Equal(t, container.Status_RUNNING, info.Status)
		// Check runtime spec
		calls := fake.GetCalledDetails()
		createOpts, ok := calls[1].Argument.(*execution.CreateRequest)
		assert.True(t, ok, "2nd call should be create")
		// TODO(random-liu): Test other create options.
		spec := &runtimespec.Spec{}
		assert.NoError(t, json.Unmarshal(createOpts.Spec.Value, spec))
		specCheck(t, testID, testSandboxPid, spec)
	}
}
