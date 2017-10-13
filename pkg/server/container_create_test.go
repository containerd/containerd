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
	"path/filepath"
	"reflect"
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/contrib/apparmor"
	"github.com/containerd/containerd/contrib/seccomp"
	"github.com/containerd/containerd/mount"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	"github.com/kubernetes-incubator/cri-containerd/pkg/util"
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
		Linux: &runtime.LinuxContainerConfig{
			Resources: &runtime.LinuxContainerResources{
				CpuPeriod:          100,
				CpuQuota:           200,
				CpuShares:          300,
				MemoryLimitInBytes: 400,
				OomScoreAdj:        500,
				CpusetCpus:         "0-1",
				CpusetMems:         "2-3",
			},
			SecurityContext: &runtime.LinuxContainerSecurityContext{
				SupplementalGroups: []int64{1111, 2222},
				NoNewPrivs:         true,
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
		Env:        []string{"ik1=iv1", "ik2=iv2", "ik3=iv3=iv3bis", "ik4=iv4=iv4bis=boop"},
		Entrypoint: []string{"/entrypoint"},
		Cmd:        []string{"cmd"},
		WorkingDir: "/workspace",
	}
	specCheck := func(t *testing.T, id string, sandboxPid uint32, spec *runtimespec.Spec) {
		assert.Equal(t, relativeRootfsPath, spec.Root.Path)
		assert.Equal(t, []string{"test", "command", "test", "args"}, spec.Process.Args)
		assert.Equal(t, "test-cwd", spec.Process.Cwd)
		assert.Contains(t, spec.Process.Env, "k1=v1", "k2=v2", "k3=v3=v3bis", "ik4=iv4=iv4bis=boop")
		assert.Contains(t, spec.Process.Env, "ik1=iv1", "ik2=iv2", "ik3=iv3=iv3bis", "k4=v4=v4bis=foop")

		t.Logf("Check cgroups bind mount")
		checkMount(t, spec.Mounts, "cgroup", "/sys/fs/cgroup", "cgroup", []string{"ro"}, nil)

		t.Logf("Check bind mount")
		checkMount(t, spec.Mounts, "host-path-1", "container-path-1", "bind", []string{"rbind", "rprivate", "rw"}, nil)
		checkMount(t, spec.Mounts, "host-path-2", "container-path-2", "bind", []string{"rbind", "rprivate", "ro"}, nil)

		t.Logf("Check resource limits")
		assert.EqualValues(t, *spec.Linux.Resources.CPU.Period, 100)
		assert.EqualValues(t, *spec.Linux.Resources.CPU.Quota, 200)
		assert.EqualValues(t, *spec.Linux.Resources.CPU.Shares, 300)
		assert.EqualValues(t, spec.Linux.Resources.CPU.Cpus, "0-1")
		assert.EqualValues(t, spec.Linux.Resources.CPU.Mems, "2-3")
		assert.EqualValues(t, *spec.Linux.Resources.Memory.Limit, 400)
		assert.EqualValues(t, *spec.Process.OOMScoreAdj, 500)

		t.Logf("Check supplemental groups")
		assert.Contains(t, spec.Process.User.AdditionalGids, uint32(1111))
		assert.Contains(t, spec.Process.User.AdditionalGids, uint32(2222))

		t.Logf("Check no_new_privs")
		assert.Equal(t, spec.Process.NoNewPrivileges, true)

		t.Logf("Check cgroup path")
		assert.Equal(t, getCgroupsPath("/test/cgroup/parent", id, false), spec.Linux.CgroupsPath)

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
	}
	return config, sandboxConfig, imageConfig, specCheck
}

func TestGeneralContainerSpec(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	c := newTestCRIContainerdService()
	spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, nil)
	require.NoError(t, err)
	specCheck(t, testID, testPid, spec)
}

func TestContainerCapabilities(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	c := newTestCRIContainerdService()
	for desc, test := range map[string]struct {
		capability *runtime.Capability
		includes   []string
		excludes   []string
	}{
		"should be able to add/drop capabilities": {
			capability: &runtime.Capability{
				AddCapabilities:  []string{"SYS_ADMIN"},
				DropCapabilities: []string{"CHOWN"},
			},
			includes: []string{"CAP_SYS_ADMIN"},
			excludes: []string{"CAP_CHOWN"},
		},
		"should be able to add all capabilities": {
			capability: &runtime.Capability{
				AddCapabilities: []string{"ALL"},
			},
			includes: getOCICapabilitiesList(),
		},
		"should be able to drop all capabilities": {
			capability: &runtime.Capability{
				DropCapabilities: []string{"ALL"},
			},
			excludes: getOCICapabilitiesList(),
		},
		"should be able to drop capabilities with add all": {
			capability: &runtime.Capability{
				AddCapabilities:  []string{"ALL"},
				DropCapabilities: []string{"CHOWN"},
			},
			includes: util.SubtractStringSlice(getOCICapabilitiesList(), "CAP_CHOWN"),
			excludes: []string{"CAP_CHOWN"},
		},
		"should be able to add capabilities with drop all": {
			capability: &runtime.Capability{
				AddCapabilities:  []string{"SYS_ADMIN"},
				DropCapabilities: []string{"ALL"},
			},
			includes: []string{"CAP_SYS_ADMIN"},
			excludes: util.SubtractStringSlice(getOCICapabilitiesList(), "CAP_SYS_ADMIN"),
		},
	} {
		t.Logf("TestCase %q", desc)
		config.Linux.SecurityContext.Capabilities = test.capability
		spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, nil)
		require.NoError(t, err)
		specCheck(t, testID, testPid, spec)
		t.Log(spec.Process.Capabilities.Bounding)
		for _, include := range test.includes {
			assert.Contains(t, spec.Process.Capabilities.Bounding, include)
			assert.Contains(t, spec.Process.Capabilities.Effective, include)
			assert.Contains(t, spec.Process.Capabilities.Inheritable, include)
			assert.Contains(t, spec.Process.Capabilities.Permitted, include)
		}
		for _, exclude := range test.excludes {
			assert.NotContains(t, spec.Process.Capabilities.Bounding, exclude)
			assert.NotContains(t, spec.Process.Capabilities.Effective, exclude)
			assert.NotContains(t, spec.Process.Capabilities.Inheritable, exclude)
			assert.NotContains(t, spec.Process.Capabilities.Permitted, exclude)
		}
	}
}

func TestContainerSpecTty(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	c := newTestCRIContainerdService()
	for _, tty := range []bool{true, false} {
		config.Tty = tty
		spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, nil)
		require.NoError(t, err)
		specCheck(t, testID, testPid, spec)
		assert.Equal(t, tty, spec.Process.Terminal)
		if tty {
			assert.Contains(t, spec.Process.Env, "TERM=xterm")
		} else {
			assert.NotContains(t, spec.Process.Env, "TERM=xterm")
		}
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
		require.NoError(t, err)
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
	require.NoError(t, err)
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

func TestGenerateVolumeMounts(t *testing.T) {
	testContainerRootDir := "test-container-root"
	for desc, test := range map[string]struct {
		criMounts         []*runtime.Mount
		imageVolumes      map[string]struct{}
		expectedMountDest []string
	}{
		"should setup rw mount for image volumes": {
			imageVolumes: map[string]struct{}{
				"/test-volume-1": {},
				"/test-volume-2": {},
			},
			expectedMountDest: []string{
				"/test-volume-1",
				"/test-volume-2",
			},
		},
		"should skip image volumes if already mounted by CRI": {
			criMounts: []*runtime.Mount{
				{
					ContainerPath: "/test-volume-1",
					HostPath:      "/test-hostpath-1",
				},
			},
			imageVolumes: map[string]struct{}{
				"/test-volume-1": {},
				"/test-volume-2": {},
			},
			expectedMountDest: []string{
				"/test-volume-2",
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		config := &imagespec.ImageConfig{
			Volumes: test.imageVolumes,
		}
		c := newTestCRIContainerdService()
		got := c.generateVolumeMounts(testContainerRootDir, test.criMounts, config)
		assert.Len(t, got, len(test.expectedMountDest))
		for _, dest := range test.expectedMountDest {
			found := false
			for _, m := range got {
				if m.ContainerPath == dest {
					found = true
					assert.Equal(t,
						filepath.Dir(m.HostPath),
						filepath.Join(testContainerRootDir, "volumes"))
					break
				}
			}
			assert.True(t, found)
		}
	}
}

func TestGenerateContainerMounts(t *testing.T) {
	testSandboxRootDir := "test-sandbox-root"
	for desc, test := range map[string]struct {
		criMounts       []*runtime.Mount
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
		"should skip contaner mounts if already mounted by CRI": {
			criMounts: []*runtime.Mount{
				{
					ContainerPath: "/etc/hosts",
					HostPath:      "/test-etc-host",
				},
				{
					ContainerPath: resolvConfPath,
					HostPath:      "test-resolv-conf",
				},
				{
					ContainerPath: "/dev/shm",
					HostPath:      "test-dev-shm",
				},
			},
			securityContext: &runtime.LinuxContainerSecurityContext{},
			expectedMounts:  nil,
		},
	} {
		config := &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{
				Name:    "test-name",
				Attempt: 1,
			},
			Mounts: test.criMounts,
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
		c := newTestCRIContainerdService()
		c.addOCIBindMounts(&g, nil, "")
		if test.privileged {
			setOCIBindMountsPrivileged(&g)
		}
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

func TestMountPropagation(t *testing.T) {
	sharedLookupMountFn := func(string) (mount.Info, error) {
		return mount.Info{
			Mountpoint: "host-path",
			Optional:   "shared:",
		}, nil
	}

	slaveLookupMountFn := func(string) (mount.Info, error) {
		return mount.Info{
			Mountpoint: "host-path",
			Optional:   "master:",
		}, nil
	}

	othersLookupMountFn := func(string) (mount.Info, error) {
		return mount.Info{
			Mountpoint: "host-path",
			Optional:   "others",
		}, nil
	}

	for desc, test := range map[string]struct {
		criMount          *runtime.Mount
		fakeLookupMountFn func(string) (mount.Info, error)
		optionsCheck      []string
		expectErr         bool
	}{
		"HostPath should mount as 'rprivate' if propagation is MountPropagation_PROPAGATION_PRIVATE": {
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_PRIVATE,
			},
			fakeLookupMountFn: nil,
			optionsCheck:      []string{"rbind", "rprivate"},
			expectErr:         false,
		},
		"HostPath should mount as 'rslave' if propagation is MountPropagation_PROPAGATION_HOST_TO_CONTAINER": {
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_HOST_TO_CONTAINER,
			},
			fakeLookupMountFn: slaveLookupMountFn,
			optionsCheck:      []string{"rbind", "rslave"},
			expectErr:         false,
		},
		"HostPath should mount as 'rshared' if propagation is MountPropagation_PROPAGATION_BIDIRECTIONAL": {
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_BIDIRECTIONAL,
			},
			fakeLookupMountFn: sharedLookupMountFn,
			optionsCheck:      []string{"rbind", "rshared"},
			expectErr:         false,
		},
		"HostPath should mount as 'rprivate' if propagation is illegal": {
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation(42),
			},
			fakeLookupMountFn: nil,
			optionsCheck:      []string{"rbind", "rprivate"},
			expectErr:         false,
		},
		"Expect an error if HostPath isn't shared and mount propagation is MountPropagation_PROPAGATION_BIDIRECTIONAL": {
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_BIDIRECTIONAL,
			},
			fakeLookupMountFn: slaveLookupMountFn,
			expectErr:         true,
		},
		"Expect an error if HostPath isn't slave or shared and mount propagation is MountPropagation_PROPAGATION_HOST_TO_CONTAINER": {
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_HOST_TO_CONTAINER,
			},
			fakeLookupMountFn: othersLookupMountFn,
			expectErr:         true,
		},
	} {
		t.Logf("TestCase %q", desc)
		g := generate.New()
		c := newTestCRIContainerdService()
		c.os.(*ostesting.FakeOS).LookupMountFn = test.fakeLookupMountFn
		err := c.addOCIBindMounts(&g, []*runtime.Mount{test.criMount}, "")
		if test.expectErr {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			checkMount(t, g.Spec().Mounts, test.criMount.HostPath, test.criMount.ContainerPath, "bind", test.optionsCheck, nil)
		}
	}
}

func TestPidNamespace(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	config, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	c := newTestCRIContainerdService()
	t.Logf("should not set pid namespace when host pid is true")
	config.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{HostPid: true}
	spec, err := c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, nil)
	require.NoError(t, err)
	specCheck(t, testID, testPid, spec)
	for _, ns := range spec.Linux.Namespaces {
		assert.NotEqual(t, ns.Type, runtimespec.PIDNamespace)
	}

	t.Logf("should set pid namespace when host pid is false")
	config.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{HostPid: false}
	spec, err = c.generateContainerSpec(testID, testPid, config, sandboxConfig, imageConfig, nil)
	require.NoError(t, err)
	specCheck(t, testID, testPid, spec)
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
		Type: runtimespec.PIDNamespace,
	})
}

func TestDefaultRuntimeSpec(t *testing.T) {
	spec, err := defaultRuntimeSpec("test-id")
	assert.NoError(t, err)
	for _, mount := range spec.Mounts {
		assert.NotEqual(t, "/run", mount.Destination)
	}
}

func TestGenerateSeccompSpecOpts(t *testing.T) {
	for desc, test := range map[string]struct {
		profile    string
		privileged bool
		disable    bool
		specOpts   containerd.SpecOpts
		expectErr  bool
	}{
		"should return error if seccomp is specified when seccomp is not supported": {
			profile:   runtimeDefault,
			disable:   true,
			expectErr: true,
		},
		"should not return error if seccomp is not specified when seccomp is not supported": {
			profile: "",
			disable: true,
		},
		"should not return error if seccomp is unconfined when seccomp is not supported": {
			profile: unconfinedProfile,
			disable: true,
		},
		"should not set seccomp when privileged is true": {
			profile:    seccompDefaultProfile,
			privileged: true,
		},
		"should not set seccomp when seccomp is unconfined": {
			profile: unconfinedProfile,
		},
		"should not set seccomp when seccomp is not specified": {
			profile: "",
		},
		"should set default seccomp when seccomp is runtime/default": {
			profile:  runtimeDefault,
			specOpts: seccomp.WithDefaultProfile(),
		},
		"should set default seccomp when seccomp is docker/default": {
			profile:  dockerDefault,
			specOpts: seccomp.WithDefaultProfile(),
		},
		"should set specified profile when local profile is specified": {
			profile:  profileNamePrefix + "test-profile",
			specOpts: seccomp.WithProfile("test-profile"),
		},
		"should return error if specified profile is invalid": {
			profile:   "test-profile",
			expectErr: true,
		},
	} {
		t.Logf("TestCase %q", desc)
		specOpts, err := generateSeccompSpecOpts(test.profile, test.privileged, !test.disable)
		assert.Equal(t,
			reflect.ValueOf(test.specOpts).Pointer(),
			reflect.ValueOf(specOpts).Pointer())
		if test.expectErr {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestGenerateApparmorSpecOpts(t *testing.T) {
	for desc, test := range map[string]struct {
		profile    string
		privileged bool
		disable    bool
		specOpts   containerd.SpecOpts
		expectErr  bool
	}{
		"should return error if apparmor is specified when apparmor is not supported": {
			profile:   runtimeDefault,
			disable:   true,
			expectErr: true,
		},
		"should not return error if apparmor is not specified when apparmor is not supported": {
			profile: "",
			disable: true,
		},
		"should set default apparmor when apparmor is not specified": {
			profile:  "",
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
		},
		"should not apparmor when apparmor is not specified and privileged is true": {
			profile:    "",
			privileged: true,
		},
		"should not return error if apparmor is unconfined when apparmor is not supported": {
			profile: unconfinedProfile,
			disable: true,
		},
		"should not apparmor when apparmor is unconfined": {
			profile: unconfinedProfile,
		},
		"should not apparmor when apparmor is unconfined and privileged is true": {
			profile:    unconfinedProfile,
			privileged: true,
		},
		"should set default apparmor when apparmor is runtime/default": {
			profile:  runtimeDefault,
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
		},
		"should set specified profile when local profile is specified": {
			profile:  profileNamePrefix + "test-profile",
			specOpts: apparmor.WithProfile("test-profile"),
		},
		"should return error if specified profile is invalid": {
			profile:   "test-profile",
			expectErr: true,
		},
	} {
		t.Logf("TestCase %q", desc)
		specOpts, err := generateApparmorSpecOpts(test.profile, test.privileged, !test.disable)
		assert.Equal(t,
			reflect.ValueOf(test.specOpts).Pointer(),
			reflect.ValueOf(specOpts).Pointer())
		if test.expectErr {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}
}
