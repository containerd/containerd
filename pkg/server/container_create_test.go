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
	"os"
	"testing"

	"github.com/containerd/containerd/api/services/containers"
	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func checkMount(t *testing.T, mounts []runtimespec.Mount, src, dest, typ string,
	contains, notcontains []string) {
	found := false
	for _, m := range mounts {
		if m.Source == src && m.Destination == dest {
			assert.Equal(t, m.Type, typ)
			for _, c := range contains {
				assert.Contains(t, m.Options, c)
			}
			for _, n := range notcontains {
				assert.NotContains(t, m.Options, n)
			}
			found = true
			break
		}
	}
	assert.True(t, found, "mount from %q to %q not found", src, dest)
}

func getCreateContainerTestData() (*runtime.ContainerConfig, *runtime.PodSandboxConfig,
	*imagespec.ImageConfig, func(*testing.T, string, uint32, *runtimespec.Spec)) {
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
					AddCapabilities:  []string{"SYS_ADMIN"},
					DropCapabilities: []string{"CHOWN"},
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
	imageConfig := &imagespec.ImageConfig{
		Env:        []string{"ik1=iv1", "ik2=iv2"},
		Entrypoint: []string{"/entrypoint"},
		Cmd:        []string{"cmd"},
		WorkingDir: "/workspace",
	}
	specCheck := func(t *testing.T, id string, sandboxPid uint32, spec *runtimespec.Spec) {
		assert.Equal(t, relativeRootfsPath, spec.Root.Path)
		assert.Equal(t, []string{"test", "command", "test", "args"}, spec.Process.Args)
		assert.Equal(t, "test-cwd", spec.Process.Cwd)
		assert.Contains(t, spec.Process.Env, "k1=v1", "k2=v2", "ik1=iv1", "ik2=iv2")

		t.Logf("Check cgroups bind mount")
		checkMount(t, spec.Mounts, "cgroup", "/sys/fs/cgroup", "cgroup", []string{"ro"}, nil)

		t.Logf("Check bind mount")
		checkMount(t, spec.Mounts, "host-path-1", "container-path-1", "bind", []string{"rw"}, nil)
		checkMount(t, spec.Mounts, "host-path-2", "container-path-2", "bind", []string{"ro"}, nil)

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
	return config, sandboxConfig, imageConfig, specCheck
}

func TestGeneralContainerSpec(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	c := newTestCRIContainerdService()
	spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, nil)
	assert.NoError(t, err)
	specCheck(t, testID, testPid, spec)
}

func TestContainerSpecTty(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	c := newTestCRIContainerdService()
	for _, tty := range []bool{true, false} {
		config.Tty = tty
		spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, nil)
		assert.NoError(t, err)
		specCheck(t, testID, testPid, spec)
		assert.Equal(t, tty, spec.Process.Terminal)
	}
}

func TestContainerSpecReadonlyRootfs(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	c := newTestCRIContainerdService()
	for _, readonly := range []bool{true, false} {
		config.Linux.SecurityContext.ReadonlyRootfs = readonly
		spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, nil)
		assert.NoError(t, err)
		specCheck(t, testID, testPid, spec)
		assert.Equal(t, readonly, spec.Root.Readonly)
	}
}

func TestContainerSpecWithExtraMounts(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	c := newTestCRIContainerdService()
	mountInConfig := &runtime.Mount{
		ContainerPath: "test-container-path",
		HostPath:      "test-host-path",
		Readonly:      false,
	}
	config.Mounts = append(config.Mounts, mountInConfig)
	extraMount := &runtime.Mount{
		ContainerPath: "test-container-path",
		HostPath:      "test-host-path-extra",
		Readonly:      true,
	}
	spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, []*runtime.Mount{extraMount})
	assert.NoError(t, err)
	specCheck(t, testID, testPid, spec)
	var mounts []runtimespec.Mount
	for _, m := range spec.Mounts {
		if m.Destination == "test-container-path" {
			mounts = append(mounts, m)
		}
	}
	t.Logf("Extra mounts should come first")
	require.Len(t, mounts, 2)
	assert.Equal(t, "test-host-path-extra", mounts[0].Source)
	assert.Contains(t, mounts[0].Options, "ro")
	assert.Equal(t, "test-host-path", mounts[1].Source)
	assert.Contains(t, mounts[1].Options, "rw")
}

func TestContainerSpecCommand(t *testing.T) {
	for desc, test := range map[string]struct {
		criEntrypoint   []string
		criArgs         []string
		imageEntrypoint []string
		imageArgs       []string
		expected        []string
		expectErr       bool
	}{
		"should use cri entrypoint if it's specified": {
			criEntrypoint:   []string{"a", "b"},
			imageEntrypoint: []string{"c", "d"},
			imageArgs:       []string{"e", "f"},
			expected:        []string{"a", "b"},
		},
		"should use cri entrypoint if it's specified even if it's empty": {
			criEntrypoint:   []string{},
			criArgs:         []string{"a", "b"},
			imageEntrypoint: []string{"c", "d"},
			imageArgs:       []string{"e", "f"},
			expected:        []string{"a", "b"},
		},
		"should use cri entrypoint and args if they are specified": {
			criEntrypoint:   []string{"a", "b"},
			criArgs:         []string{"c", "d"},
			imageEntrypoint: []string{"e", "f"},
			imageArgs:       []string{"g", "h"},
			expected:        []string{"a", "b", "c", "d"},
		},
		"should use image entrypoint if cri entrypoint is not specified": {
			criArgs:         []string{"a", "b"},
			imageEntrypoint: []string{"c", "d"},
			imageArgs:       []string{"e", "f"},
			expected:        []string{"c", "d", "a", "b"},
		},
		"should use image args if both cri entrypoint and args are not specified": {
			imageEntrypoint: []string{"c", "d"},
			imageArgs:       []string{"e", "f"},
			expected:        []string{"c", "d", "e", "f"},
		},
		"should return error if both entrypoint and args are empty": {
			expectErr: true,
		},
	} {

		config, _, imageConfig, _ := getCreateContainerTestData()
		g := generate.New()
		config.Command = test.criEntrypoint
		config.Args = test.criArgs
		imageConfig.Entrypoint = test.imageEntrypoint
		imageConfig.Cmd = test.imageArgs
		err := setOCIProcessArgs(&g, config, imageConfig)
		if test.expectErr {
			assert.Error(t, err)
			continue
		}
		assert.NoError(t, err)
		assert.Equal(t, test.expected, g.Spec().Process.Args, desc)
	}
}

func TestGenerateContainerMounts(t *testing.T) {
	testSandboxRootDir := "test-sandbox-root"
	for desc, test := range map[string]struct {
		securityContext *runtime.LinuxContainerSecurityContext
		expectedMounts  []*runtime.Mount
	}{
		"should setup ro mount when rootfs is read-only": {
			securityContext: &runtime.LinuxContainerSecurityContext{
				ReadonlyRootfs: true,
			},
			expectedMounts: []*runtime.Mount{
				{
					ContainerPath: "/etc/hosts",
					HostPath:      testSandboxRootDir + "/hosts",
					Readonly:      true,
				},
				{
					ContainerPath: resolvConfPath,
					HostPath:      testSandboxRootDir + "/resolv.conf",
					Readonly:      true,
				},
				{
					ContainerPath: "/dev/shm",
					HostPath:      testSandboxRootDir + "/shm",
					Readonly:      false,
				},
			},
		},
		"should setup rw mount when rootfs is read-write": {
			securityContext: &runtime.LinuxContainerSecurityContext{},
			expectedMounts: []*runtime.Mount{
				{
					ContainerPath: "/etc/hosts",
					HostPath:      testSandboxRootDir + "/hosts",
					Readonly:      false,
				},
				{
					ContainerPath: resolvConfPath,
					HostPath:      testSandboxRootDir + "/resolv.conf",
					Readonly:      false,
				},
				{
					ContainerPath: "/dev/shm",
					HostPath:      testSandboxRootDir + "/shm",
					Readonly:      false,
				},
			},
		},
		"should use host /dev/shm when host ipc is set": {
			securityContext: &runtime.LinuxContainerSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{HostIpc: true},
			},
			expectedMounts: []*runtime.Mount{
				{
					ContainerPath: "/etc/hosts",
					HostPath:      testSandboxRootDir + "/hosts",
					Readonly:      false,
				},
				{
					ContainerPath: resolvConfPath,
					HostPath:      testSandboxRootDir + "/resolv.conf",
					Readonly:      false,
				},
				{
					ContainerPath: "/dev/shm",
					HostPath:      "/dev/shm",
					Readonly:      false,
				},
			},
		},
	} {
		config := &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{
				Name:    "test-name",
				Attempt: 1,
			},
			Linux: &runtime.LinuxContainerConfig{
				SecurityContext: test.securityContext,
			},
		}
		c := newTestCRIContainerdService()
		mounts := c.generateContainerMounts(testSandboxRootDir, config)
		assert.Equal(t, test.expectedMounts, mounts, desc)
	}
}

func TestPrivilegedBindMount(t *testing.T) {
	for desc, test := range map[string]struct {
		privileged         bool
		readonlyRootFS     bool
		expectedSysFSRO    bool
		expectedCgroupFSRO bool
	}{
		"sysfs and cgroupfs should mount as 'ro' by default": {
			expectedSysFSRO:    true,
			expectedCgroupFSRO: true,
		},
		"sysfs and cgroupfs should not mount as 'ro' if privileged": {
			privileged:         true,
			expectedSysFSRO:    false,
			expectedCgroupFSRO: false,
		},
		"sysfs should mount as 'ro' if root filrsystem is readonly": {
			privileged:         true,
			readonlyRootFS:     true,
			expectedSysFSRO:    true,
			expectedCgroupFSRO: false,
		},
	} {
		t.Logf("TestCase %q", desc)
		g := generate.New()
		g.SetRootReadonly(test.readonlyRootFS)
		addOCIBindMounts(&g, nil, test.privileged)
		spec := g.Spec()
		if test.expectedSysFSRO {
			checkMount(t, spec.Mounts, "sysfs", "/sys", "sysfs", []string{"ro"}, nil)
		} else {
			checkMount(t, spec.Mounts, "sysfs", "/sys", "sysfs", nil, []string{"ro"})
		}
		if test.expectedCgroupFSRO {
			checkMount(t, spec.Mounts, "cgroup", "/sys/fs/cgroup", "cgroup", []string{"ro"}, nil)
		} else {
			checkMount(t, spec.Mounts, "cgroup", "/sys/fs/cgroup", "cgroup", nil, []string{"ro"})
		}
	}
}

func TestCreateContainer(t *testing.T) {
	testSandboxID := "test-sandbox-id"
	testSandboxPid := uint32(4321)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	testSandboxMetadata := &metadata.SandboxMetadata{
		ID:     testSandboxID,
		Name:   "test-sandbox-name",
		Config: sandboxConfig,
		Pid:    testSandboxPid,
	}
	testContainerName := makeContainerName(config.Metadata, sandboxConfig.Metadata)
	// Use an image id to avoid image name resolution.
	// TODO(random-liu): Change this to image name after we have complete image
	// management unit test framework.
	testImage := config.GetImage().GetImage()
	testChainID := "test-chain-id"
	testImageMetadata := metadata.ImageMetadata{
		ID:      testImage,
		ChainID: testChainID,
		Config:  imageConfig,
	}

	for desc, test := range map[string]struct {
		sandboxMetadata    *metadata.SandboxMetadata
		reserveNameErr     bool
		imageMetadataErr   bool
		prepareSnapshotErr error
		createRootDirErr   error
		createMetadataErr  bool
		expectErr          bool
		expectMeta         *metadata.ContainerMetadata
	}{
		"should return error if sandbox does not exist": {
			sandboxMetadata: nil,
			expectErr:       true,
		},
		"should return error if name is reserved": {
			sandboxMetadata: testSandboxMetadata,
			reserveNameErr:  true,
			expectErr:       true,
		},
		"should return error if fail to create root directory": {
			sandboxMetadata:  testSandboxMetadata,
			createRootDirErr: errors.New("random error"),
			expectErr:        true,
		},
		"should return error if image is not pulled": {
			sandboxMetadata:  testSandboxMetadata,
			imageMetadataErr: true,
			expectErr:        true,
		},
		"should return error if prepare snapshot fails": {
			sandboxMetadata:    testSandboxMetadata,
			prepareSnapshotErr: errors.New("random error"),
			expectErr:          true,
		},
		"should be able to create container successfully": {
			sandboxMetadata: testSandboxMetadata,
			expectErr:       false,
			expectMeta: &metadata.ContainerMetadata{
				Name:      testContainerName,
				SandboxID: testSandboxID,
				ImageRef:  testImage,
				Config:    config,
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := c.containerService.(*servertesting.FakeContainersClient)
		fakeSnapshotClient := WithFakeSnapshotClient(c)
		fakeOS := c.os.(*ostesting.FakeOS)
		if test.sandboxMetadata != nil {
			assert.NoError(t, c.sandboxStore.Create(*test.sandboxMetadata))
		}
		if test.reserveNameErr {
			assert.NoError(t, c.containerNameIndex.Reserve(testContainerName, "random id"))
		}
		if !test.imageMetadataErr {
			assert.NoError(t, c.imageMetadataStore.Create(testImageMetadata))
		}
		if test.prepareSnapshotErr != nil {
			fakeSnapshotClient.InjectError("prepare", test.prepareSnapshotErr)
		}
		rootExists := false
		rootPath := ""
		fakeOS.MkdirAllFn = func(path string, perm os.FileMode) error {
			assert.Equal(t, os.FileMode(0755), perm)
			rootPath = path
			if test.createRootDirErr == nil {
				rootExists = true
			}
			return test.createRootDirErr
		}
		fakeOS.RemoveAllFn = func(path string) error {
			assert.Equal(t, rootPath, path)
			rootExists = false
			return nil
		}
		resp, err := c.CreateContainer(context.Background(), &runtime.CreateContainerRequest{
			PodSandboxId:  testSandboxID,
			Config:        config,
			SandboxConfig: sandboxConfig,
		})
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, resp)
			assert.False(t, rootExists, "root directory should be cleaned up")
			if !test.reserveNameErr {
				assert.NoError(t, c.containerNameIndex.Reserve(testContainerName, "random id"),
					"container name should be released")
			}
			assert.Empty(t, fakeSnapshotClient.ListMounts(), "snapshot should be cleaned up")
			listResp, err := fake.List(context.Background(), &containers.ListContainersRequest{})
			assert.NoError(t, err)
			assert.Empty(t, listResp.Containers, "containerd container should be cleaned up")
			metas, err := c.containerStore.List()
			assert.NoError(t, err)
			assert.Empty(t, metas, "container metadata should not be created")
			continue
		}
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		id := resp.GetContainerId()
		assert.True(t, rootExists)
		assert.Equal(t, getContainerRootDir(c.rootDir, id), rootPath, "root directory should be created")
		meta, err := c.containerStore.Get(id)
		assert.NoError(t, err)
		require.NotNil(t, meta)
		test.expectMeta.ID = id
		// TODO(random-liu): Use fake clock to test CreatedAt.
		test.expectMeta.CreatedAt = meta.CreatedAt
		assert.Equal(t, test.expectMeta, meta, "container metadata should be created")

		// Check runtime spec
		containersCalls := fake.GetCalledDetails()
		require.Len(t, containersCalls, 1)
		createOpts, ok := containersCalls[0].Argument.(*containers.CreateContainerRequest)
		assert.True(t, ok, "should create containerd container")
		assert.Equal(t, id, createOpts.Container.ID, "container id should be correct")
		assert.Equal(t, testImage, createOpts.Container.Image, "test image should be correct")
		assert.Equal(t, id, createOpts.Container.RootFS, "rootfs should be correct")
		spec := &runtimespec.Spec{}
		assert.NoError(t, json.Unmarshal(createOpts.Container.Spec.Value, spec))
		specCheck(t, id, testSandboxPid, spec)

		assert.Equal(t, []string{"prepare"}, fakeSnapshotClient.GetCalledNames(), "prepare should be called")
		snapshotCalls := fakeSnapshotClient.GetCalledDetails()
		require.Len(t, snapshotCalls, 1)
		prepareOpts := snapshotCalls[0].Argument.(*snapshotapi.PrepareRequest)
		assert.Equal(t, &snapshotapi.PrepareRequest{
			Key:    id,
			Parent: testChainID,
		}, prepareOpts, "prepare request should be correct")
	}
}
