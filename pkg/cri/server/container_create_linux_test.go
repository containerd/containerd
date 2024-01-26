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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/container-orchestrated-devices/container-device-interface/pkg/cdi"
	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/contrib/apparmor"
	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/platforms"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/pkg/cap"
	"github.com/containerd/containerd/v2/pkg/cri/annotations"
	"github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/opts"
	customopts "github.com/containerd/containerd/v2/pkg/cri/opts"
	"github.com/containerd/containerd/v2/pkg/cri/util"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	ostesting "github.com/containerd/containerd/v2/pkg/os/testing"
)

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
		Annotations: map[string]string{"ca-c": "ca-d"},
		Linux: &runtime.LinuxContainerConfig{
			Resources: &runtime.LinuxContainerResources{
				CpuPeriod:          100,
				CpuQuota:           200,
				CpuShares:          300,
				MemoryLimitInBytes: 400,
				OomScoreAdj:        500,
				CpusetCpus:         "0-1",
				CpusetMems:         "2-3",
				Unified:            map[string]string{"memory.min": "65536", "memory.swap.max": "1024"},
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
		Annotations: map[string]string{"c": "d"},
		Linux: &runtime.LinuxPodSandboxConfig{
			CgroupParent:    "/test/cgroup/parent",
			SecurityContext: &runtime.LinuxSandboxSecurityContext{},
		},
	}
	imageConfig := &imagespec.ImageConfig{
		Env:        []string{"ik1=iv1", "ik2=iv2", "ik3=iv3=iv3bis", "ik4=iv4=iv4bis=boop"},
		Entrypoint: []string{"/entrypoint"},
		Cmd:        []string{"cmd"},
		WorkingDir: "/workspace",
	}
	specCheck := func(t *testing.T, id string, sandboxID string, sandboxPid uint32, spec *runtimespec.Spec) {
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
		assert.EqualValues(t, spec.Linux.Resources.Unified, map[string]string{"memory.min": "65536", "memory.swap.max": "1024"})
		assert.EqualValues(t, *spec.Linux.Resources.Memory.Limit, 400)
		assert.EqualValues(t, *spec.Process.OOMScoreAdj, 500)

		t.Logf("Check supplemental groups")
		assert.Contains(t, spec.Process.User.AdditionalGids, uint32(1111))
		assert.Contains(t, spec.Process.User.AdditionalGids, uint32(2222))

		t.Logf("Check no_new_privs")
		assert.Equal(t, spec.Process.NoNewPrivileges, true)

		t.Logf("Check cgroup path")
		assert.Equal(t, getCgroupsPath("/test/cgroup/parent", id), spec.Linux.CgroupsPath)

		t.Logf("Check namespaces")
		assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: runtimespec.NetworkNamespace,
			Path: opts.GetNetworkNamespace(sandboxPid),
		})
		assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: runtimespec.IPCNamespace,
			Path: opts.GetIPCNamespace(sandboxPid),
		})
		assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: runtimespec.UTSNamespace,
			Path: opts.GetUTSNamespace(sandboxPid),
		})
		assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: runtimespec.PIDNamespace,
			Path: opts.GetPIDNamespace(sandboxPid),
		})

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

		assert.Contains(t, spec.Annotations, annotations.ImageName)
		assert.EqualValues(t, spec.Annotations[annotations.ImageName], testImageName)
	}
	return config, sandboxConfig, imageConfig, specCheck
}

func TestContainerCapabilities(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	allCaps := cap.Known()
	for _, test := range []struct {
		desc       string
		capability *runtime.Capability
		includes   []string
		excludes   []string
	}{
		{
			desc: "should be able to add/drop capabilities",
			capability: &runtime.Capability{
				AddCapabilities:  []string{"SYS_ADMIN"},
				DropCapabilities: []string{"CHOWN"},
			},
			includes: []string{"CAP_SYS_ADMIN"},
			excludes: []string{"CAP_CHOWN"},
		},
		{
			desc: "should be able to add all capabilities",
			capability: &runtime.Capability{
				AddCapabilities: []string{"ALL"},
			},
			includes: allCaps,
		},
		{
			desc: "should be able to drop all capabilities",
			capability: &runtime.Capability{
				DropCapabilities: []string{"ALL"},
			},
			excludes: allCaps,
		},
		{
			desc: "should be able to drop capabilities with add all",
			capability: &runtime.Capability{
				AddCapabilities:  []string{"ALL"},
				DropCapabilities: []string{"CHOWN"},
			},
			includes: util.SubtractStringSlice(allCaps, "CAP_CHOWN"),
			excludes: []string{"CAP_CHOWN"},
		},
		{
			desc: "should be able to add capabilities with drop all",
			capability: &runtime.Capability{
				AddCapabilities:  []string{"SYS_ADMIN"},
				DropCapabilities: []string{"ALL"},
			},
			includes: []string{"CAP_SYS_ADMIN"},
			excludes: util.SubtractStringSlice(allCaps, "CAP_SYS_ADMIN"),
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
			ociRuntime := config.Runtime{}
			c := newTestCRIService()
			c.allCaps = allCaps

			containerConfig.Linux.SecurityContext.Capabilities = test.capability
			spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			require.NoError(t, err)

			if selinux.GetEnabled() {
				assert.NotEqual(t, "", spec.Process.SelinuxLabel)
				assert.NotEqual(t, "", spec.Linux.MountLabel)
			}

			specCheck(t, testID, testSandboxID, testPid, spec)
			for _, include := range test.includes {
				assert.Contains(t, spec.Process.Capabilities.Bounding, include)
				assert.Contains(t, spec.Process.Capabilities.Effective, include)
				assert.Contains(t, spec.Process.Capabilities.Permitted, include)
			}
			for _, exclude := range test.excludes {
				assert.NotContains(t, spec.Process.Capabilities.Bounding, exclude)
				assert.NotContains(t, spec.Process.Capabilities.Effective, exclude)
				assert.NotContains(t, spec.Process.Capabilities.Permitted, exclude)
			}
			assert.Empty(t, spec.Process.Capabilities.Inheritable)
			assert.Empty(t, spec.Process.Capabilities.Ambient)
		})
	}
}

func TestContainerSpecTty(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	for _, tty := range []bool{true, false} {
		containerConfig.Tty = tty
		spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
		require.NoError(t, err)
		specCheck(t, testID, testSandboxID, testPid, spec)
		assert.Equal(t, tty, spec.Process.Terminal)
		if tty {
			assert.Contains(t, spec.Process.Env, "TERM=xterm")
		} else {
			assert.NotContains(t, spec.Process.Env, "TERM=xterm")
		}
	}
}

func TestContainerSpecDefaultPath(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	expectedDefault := "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	for _, pathenv := range []string{"", "PATH=/usr/local/bin/games"} {
		expected := expectedDefault
		if pathenv != "" {
			imageConfig.Env = append(imageConfig.Env, pathenv)
			expected = pathenv
		}
		spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
		require.NoError(t, err)
		specCheck(t, testID, testSandboxID, testPid, spec)
		assert.Contains(t, spec.Process.Env, expected)
	}
}

func TestContainerSpecReadonlyRootfs(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	for _, readonly := range []bool{true, false} {
		containerConfig.Linux.SecurityContext.ReadonlyRootfs = readonly
		spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
		require.NoError(t, err)
		specCheck(t, testID, testSandboxID, testPid, spec)
		assert.Equal(t, readonly, spec.Root.Readonly)
	}
}

func TestContainerSpecWithExtraMounts(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	mountInConfig := &runtime.Mount{
		// Test cleanpath
		ContainerPath: "test-container-path/",
		HostPath:      "test-host-path",
		Readonly:      false,
	}
	containerConfig.Mounts = append(containerConfig.Mounts, mountInConfig)
	extraMounts := []*runtime.Mount{
		{
			ContainerPath: "test-container-path",
			HostPath:      "test-host-path-extra",
			Readonly:      true,
		},
		{
			ContainerPath: "/sys",
			HostPath:      "test-sys-extra",
			Readonly:      false,
		},
	}
	spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, extraMounts, ociRuntime)
	require.NoError(t, err)
	specCheck(t, testID, testSandboxID, testPid, spec)
	var mounts, sysMounts []runtimespec.Mount
	for _, m := range spec.Mounts {
		if strings.HasPrefix(m.Destination, "test-container-path") {
			mounts = append(mounts, m)
		} else if m.Destination == "/sys" {
			sysMounts = append(sysMounts, m)
		}
	}
	t.Logf("CRI mount should override extra mount")
	require.Len(t, mounts, 1)
	assert.Equal(t, "test-host-path", mounts[0].Source)
	assert.Contains(t, mounts[0].Options, "rw")

	t.Logf("Extra mount should override default mount")
	require.Len(t, sysMounts, 1)
	assert.Equal(t, "test-sys-extra", sysMounts[0].Source)
	assert.Contains(t, sysMounts[0].Options, "rw")
}

func TestContainerAndSandboxPrivileged(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	for _, test := range []struct {
		desc                string
		containerPrivileged bool
		sandboxPrivileged   bool
		expectError         bool
	}{
		{
			desc:                "privileged container in non-privileged sandbox should fail",
			containerPrivileged: true,
			sandboxPrivileged:   false,
			expectError:         true,
		},
		{
			desc:                "privileged container in privileged sandbox should be fine",
			containerPrivileged: true,
			sandboxPrivileged:   true,
			expectError:         false,
		},
		{
			desc:                "non-privileged container in privileged sandbox should be fine",
			containerPrivileged: false,
			sandboxPrivileged:   true,
			expectError:         false,
		},
		{
			desc:                "non-privileged container in non-privileged sandbox should be fine",
			containerPrivileged: false,
			sandboxPrivileged:   false,
			expectError:         false,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			containerConfig.Linux.SecurityContext.Privileged = test.containerPrivileged
			sandboxConfig.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
				Privileged: test.sandboxPrivileged,
			}
			_, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPrivilegedBindMount(t *testing.T) {
	testPid := uint32(1234)
	c := newTestCRIService()
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}

	for _, test := range []struct {
		desc               string
		privileged         bool
		expectedSysFSRO    bool
		expectedCgroupFSRO bool
	}{
		{
			desc:               "sysfs and cgroupfs should mount as 'ro' by default",
			expectedSysFSRO:    true,
			expectedCgroupFSRO: true,
		},
		{
			desc:               "sysfs and cgroupfs should not mount as 'ro' if privileged",
			privileged:         true,
			expectedSysFSRO:    false,
			expectedCgroupFSRO: false,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			containerConfig.Linux.SecurityContext.Privileged = test.privileged
			sandboxConfig.Linux.SecurityContext.Privileged = test.privileged

			spec, err := c.buildContainerSpec(currentPlatform, t.Name(), testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)

			assert.NoError(t, err)
			if test.expectedSysFSRO {
				checkMount(t, spec.Mounts, "sysfs", "/sys", "sysfs", []string{"ro"}, []string{"rw"})
			} else {
				checkMount(t, spec.Mounts, "sysfs", "/sys", "sysfs", []string{"rw"}, []string{"ro"})
			}
			if test.expectedCgroupFSRO {
				checkMount(t, spec.Mounts, "cgroup", "/sys/fs/cgroup", "cgroup", []string{"ro"}, []string{"rw"})
			} else {
				checkMount(t, spec.Mounts, "cgroup", "/sys/fs/cgroup", "cgroup", []string{"rw"}, []string{"ro"})
			}
		})
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

	for _, test := range []struct {
		desc              string
		criMount          *runtime.Mount
		fakeLookupMountFn func(string) (mount.Info, error)
		optionsCheck      []string
		expectErr         bool
	}{
		{
			desc: "HostPath should mount as 'rprivate' if propagation is MountPropagation_PROPAGATION_PRIVATE",
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_PRIVATE,
			},
			fakeLookupMountFn: nil,
			optionsCheck:      []string{"rbind", "rprivate"},
			expectErr:         false,
		},
		{
			desc: "HostPath should mount as 'rslave' if propagation is MountPropagation_PROPAGATION_HOST_TO_CONTAINER",
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_HOST_TO_CONTAINER,
			},
			fakeLookupMountFn: slaveLookupMountFn,
			optionsCheck:      []string{"rbind", "rslave"},
			expectErr:         false,
		},
		{
			desc: "HostPath should mount as 'rshared' if propagation is MountPropagation_PROPAGATION_BIDIRECTIONAL",
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_BIDIRECTIONAL,
			},
			fakeLookupMountFn: sharedLookupMountFn,
			optionsCheck:      []string{"rbind", "rshared"},
			expectErr:         false,
		},
		{
			desc: "HostPath should mount as 'rprivate' if propagation is illegal",
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation(42),
			},
			fakeLookupMountFn: nil,
			optionsCheck:      []string{"rbind", "rprivate"},
			expectErr:         false,
		},
		{
			desc: "Expect an error if HostPath isn't shared and mount propagation is MountPropagation_PROPAGATION_BIDIRECTIONAL",
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_BIDIRECTIONAL,
			},
			fakeLookupMountFn: slaveLookupMountFn,
			expectErr:         true,
		},
		{
			desc: "Expect an error if HostPath isn't slave or shared and mount propagation is MountPropagation_PROPAGATION_HOST_TO_CONTAINER",
			criMount: &runtime.Mount{
				ContainerPath: "container-path",
				HostPath:      "host-path",
				Propagation:   runtime.MountPropagation_PROPAGATION_HOST_TO_CONTAINER,
			},
			fakeLookupMountFn: othersLookupMountFn,
			expectErr:         true,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			c.os.(*ostesting.FakeOS).LookupMountFn = test.fakeLookupMountFn
			config, _, _, _ := getCreateContainerTestData()

			var spec runtimespec.Spec
			spec.Linux = &runtimespec.Linux{}

			err := opts.WithMounts(c.os, config, []*runtime.Mount{test.criMount}, "")(context.Background(), nil, nil, &spec)
			if test.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				checkMount(t, spec.Mounts, test.criMount.HostPath, test.criMount.ContainerPath, "bind", test.optionsCheck, nil)
			}
		})
	}
}

func TestPidNamespace(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	for _, test := range []struct {
		desc     string
		pidNS    runtime.NamespaceMode
		expected runtimespec.LinuxNamespace
	}{
		{
			desc:  "node namespace mode",
			pidNS: runtime.NamespaceMode_NODE,
			expected: runtimespec.LinuxNamespace{
				Type: runtimespec.PIDNamespace,
				Path: opts.GetPIDNamespace(testPid),
			},
		},
		{
			desc:  "container namespace mode",
			pidNS: runtime.NamespaceMode_CONTAINER,
			expected: runtimespec.LinuxNamespace{
				Type: runtimespec.PIDNamespace,
			},
		},
		{
			desc:  "pod namespace mode",
			pidNS: runtime.NamespaceMode_POD,
			expected: runtimespec.LinuxNamespace{
				Type: runtimespec.PIDNamespace,
				Path: opts.GetPIDNamespace(testPid),
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			containerConfig.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{Pid: test.pidNS}
			spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			require.NoError(t, err)
			assert.Contains(t, spec.Linux.Namespaces, test.expected)
		})
	}
}

func TestUserNamespace(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	idMap := runtime.IDMapping{
		HostId:      1000,
		ContainerId: 1000,
		Length:      10,
	}
	otherIDMap := runtime.IDMapping{
		HostId:      2000,
		ContainerId: 2000,
		Length:      10,
	}
	expIDMap := runtimespec.LinuxIDMapping{
		HostID:      1000,
		ContainerID: 1000,
		Size:        10,
	}
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()

	for _, test := range []struct {
		desc          string
		userNS        *runtime.UserNamespace
		sandboxUserNS *runtime.UserNamespace
		expNS         *runtimespec.LinuxNamespace
		expNotNS      *runtimespec.LinuxNamespace // Does NOT contain this namespace
		expUIDMapping []runtimespec.LinuxIDMapping
		expGIDMapping []runtimespec.LinuxIDMapping
		err           bool
	}{
		{
			desc:   "node namespace mode",
			userNS: &runtime.UserNamespace{Mode: runtime.NamespaceMode_NODE},
			// Expect userns to NOT be present.
			expNotNS: &runtimespec.LinuxNamespace{
				Type: runtimespec.UserNamespace,
				Path: opts.GetUserNamespace(testPid),
			},
		},
		{
			desc: "node namespace mode with mappings",
			userNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_NODE,
				Uids: []*runtime.IDMapping{&idMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			err: true,
		},
		{
			desc:   "container namespace mode",
			userNS: &runtime.UserNamespace{Mode: runtime.NamespaceMode_CONTAINER},
			err:    true,
		},
		{
			desc:   "target namespace mode",
			userNS: &runtime.UserNamespace{Mode: runtime.NamespaceMode_TARGET},
			err:    true,
		},
		{
			desc:   "unknown namespace mode",
			userNS: &runtime.UserNamespace{Mode: runtime.NamespaceMode(100)},
			err:    true,
		},
		{
			desc: "pod namespace mode",
			userNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			expNS: &runtimespec.LinuxNamespace{
				Type: runtimespec.UserNamespace,
				Path: opts.GetUserNamespace(testPid),
			},
			expUIDMapping: []runtimespec.LinuxIDMapping{expIDMap},
			expGIDMapping: []runtimespec.LinuxIDMapping{expIDMap},
		},
		{
			desc: "pod namespace mode with inconsistent sandbox config (different GIDs)",
			userNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			sandboxUserNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap},
				Gids: []*runtime.IDMapping{&otherIDMap},
			},
			err: true,
		},
		{
			desc: "pod namespace mode with inconsistent sandbox config (different UIDs)",
			userNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			sandboxUserNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&otherIDMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			err: true,
		},
		{
			desc: "pod namespace mode with inconsistent sandbox config (different len)",
			userNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			sandboxUserNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap, &idMap},
				Gids: []*runtime.IDMapping{&idMap, &idMap},
			},
			err: true,
		},
		{
			desc: "pod namespace mode with inconsistent sandbox config (different mode)",
			userNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			sandboxUserNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_NODE,
				Uids: []*runtime.IDMapping{&idMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			err: true,
		},
		{
			desc: "pod namespace mode with several mappings",
			userNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap, &idMap},
				Gids: []*runtime.IDMapping{&idMap, &idMap},
			},
			err: true,
		},
		{
			desc: "pod namespace mode with uneven mappings",
			userNS: &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
				Uids: []*runtime.IDMapping{&idMap, &idMap},
				Gids: []*runtime.IDMapping{&idMap},
			},
			err: true,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			containerConfig.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{UsernsOptions: test.userNS}
			// By default, set sandbox and container config to the same (this is
			// required by containerSpec). However, if the test wants to test for what
			// happens when they don't match, the test.sandboxUserNS should be set and
			// we just use that.
			sandboxUserns := test.userNS
			if test.sandboxUserNS != nil {
				sandboxUserns = test.sandboxUserNS
			}
			sandboxConfig.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{UsernsOptions: sandboxUserns}
			spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)

			if test.err {
				require.Error(t, err)
				assert.Nil(t, spec)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, spec.Linux.UIDMappings, test.expUIDMapping)
			assert.Equal(t, spec.Linux.GIDMappings, test.expGIDMapping)

			if test.expNS != nil {
				assert.Contains(t, spec.Linux.Namespaces, *test.expNS)
			}
			if test.expNotNS != nil {
				assert.NotContains(t, spec.Linux.Namespaces, *test.expNotNS)
			}
		})
	}
}

func TestNoDefaultRunMount(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()

	spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
	assert.NoError(t, err)
	for _, mount := range spec.Mounts {
		assert.NotEqual(t, "/run", mount.Destination)
	}
}

func TestGenerateSeccompSecurityProfileSpecOpts(t *testing.T) {
	for _, test := range []struct {
		desc           string
		profile        string
		privileged     bool
		disable        bool
		specOpts       oci.SpecOpts
		expectErr      bool
		defaultProfile string
		sp             *runtime.SecurityProfile
	}{
		{
			desc:      "should return error if seccomp is specified when seccomp is not supported",
			profile:   runtimeDefault,
			disable:   true,
			expectErr: true,
		},
		{
			desc:    "should not return error if seccomp is not specified when seccomp is not supported",
			profile: "",
			disable: true,
		},
		{
			desc:    "should not return error if seccomp is unconfined when seccomp is not supported",
			profile: unconfinedProfile,
			disable: true,
		},
		{
			desc:       "should not set seccomp when privileged is true",
			profile:    seccompDefaultProfile,
			privileged: true,
		},
		{
			desc:    "should not set seccomp when seccomp is unconfined",
			profile: unconfinedProfile,
		},
		{
			desc:    "should not set seccomp when seccomp is not specified",
			profile: "",
		},
		{
			desc:     "should set default seccomp when seccomp is runtime/default",
			profile:  runtimeDefault,
			specOpts: seccomp.WithDefaultProfile(),
		},
		{
			desc:     "should set default seccomp when seccomp is docker/default",
			profile:  dockerDefault,
			specOpts: seccomp.WithDefaultProfile(),
		},
		{
			desc:     "should set specified profile when local profile is specified",
			profile:  profileNamePrefix + "test-profile",
			specOpts: seccomp.WithProfile("test-profile"),
		},
		{
			desc:           "should use default profile when seccomp is empty",
			defaultProfile: profileNamePrefix + "test-profile",
			specOpts:       seccomp.WithProfile("test-profile"),
		},
		{
			desc:           "should fallback to docker/default when seccomp is empty and default is runtime/default",
			defaultProfile: runtimeDefault,
			specOpts:       seccomp.WithDefaultProfile(),
		},
		//-----------------------------------------------
		// now buckets for the SecurityProfile variants
		//-----------------------------------------------
		{
			desc:      "sp should return error if seccomp is specified when seccomp is not supported",
			disable:   true,
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:    "sp should not return error if seccomp is unconfined when seccomp is not supported",
			disable: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc:       "sp should not set seccomp when privileged is true",
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc: "sp should not set seccomp when seccomp is unconfined",
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc: "sp should not set seccomp when seccomp is not specified",
		},
		{
			desc:     "sp should set default seccomp when seccomp is runtime/default",
			specOpts: seccomp.WithDefaultProfile(),
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:     "sp should set specified profile when local profile is specified",
			specOpts: seccomp.WithProfile("test-profile"),
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
		{
			desc:     "sp should set specified profile when local profile is specified even without prefix",
			specOpts: seccomp.WithProfile("test-profile"),
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: "test-profile",
			},
		},
		{
			desc:      "sp should return error if specified profile is invalid",
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_RuntimeDefault,
				LocalhostRef: "test-profile",
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			cri := &criService{}
			cri.config.UnsetSeccompProfile = test.defaultProfile
			ssp := test.sp
			csp, err := generateSeccompSecurityProfile(
				test.profile,
				test.defaultProfile)
			if err != nil {
				if test.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			} else {
				if ssp == nil {
					ssp = csp
				}
				specOpts, err := cri.generateSeccompSpecOpts(ssp, test.privileged, !test.disable)
				assert.Equal(t,
					reflect.ValueOf(test.specOpts).Pointer(),
					reflect.ValueOf(specOpts).Pointer())
				if test.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}

func TestGenerateApparmorSpecOpts(t *testing.T) {
	for _, test := range []struct {
		desc       string
		profile    string
		privileged bool
		disable    bool
		specOpts   oci.SpecOpts
		expectErr  bool
		sp         *runtime.SecurityProfile
	}{
		{
			desc:      "should return error if apparmor is specified when apparmor is not supported",
			profile:   runtimeDefault,
			disable:   true,
			expectErr: true,
		},
		{
			desc:    "should not return error if apparmor is not specified when apparmor is not supported",
			profile: "",
			disable: true,
		},
		{
			desc:     "should set default apparmor when apparmor is not specified",
			profile:  "",
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
		},
		{
			desc:       "should not apparmor when apparmor is not specified and privileged is true",
			profile:    "",
			privileged: true,
		},
		{
			desc:    "should not return error if apparmor is unconfined when apparmor is not supported",
			profile: unconfinedProfile,
			disable: true,
		},
		{
			desc:    "should not apparmor when apparmor is unconfined",
			profile: unconfinedProfile,
		},
		{
			desc:       "should not apparmor when apparmor is unconfined and privileged is true",
			profile:    unconfinedProfile,
			privileged: true,
		},
		{
			desc:     "should set default apparmor when apparmor is runtime/default",
			profile:  runtimeDefault,
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
		},
		{
			desc:       "should not apparmor when apparmor is default and privileged is true",
			profile:    runtimeDefault,
			privileged: true,
		},
		// TODO (mikebrow) add success with existing defined profile tests
		{
			desc:      "should return error when undefined local profile is specified",
			profile:   profileNamePrefix + "test-profile",
			expectErr: true,
		},
		{
			desc:       "should return error when undefined local profile is specified and privileged is true",
			profile:    profileNamePrefix + "test-profile",
			privileged: true,
			expectErr:  true,
		},
		{
			desc:      "should return error if specified profile is invalid",
			profile:   "test-profile",
			expectErr: true,
		},
		//--------------------------------------
		// buckets for SecurityProfile struct
		//--------------------------------------
		{
			desc:      "sp should return error if apparmor is specified when apparmor is not supported",
			disable:   true,
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:    "sp should not return error if apparmor is unconfined when apparmor is not supported",
			disable: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc: "sp should not apparmor when apparmor is unconfined",
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc:       "sp should not apparmor when apparmor is unconfined and privileged is true",
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc:     "sp should set default apparmor when apparmor is runtime/default",
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:       "sp should not apparmor when apparmor is default and privileged is true",
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:      "sp should return error when undefined local profile is specified",
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
		{
			desc:      "sp should return error when undefined local profile is specified even without prefix",
			profile:   profileNamePrefix + "test-profile",
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: "test-profile",
			},
		},
		{
			desc:       "sp should return error when undefined local profile is specified and privileged is true",
			privileged: true,
			expectErr:  true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			asp := test.sp
			csp, err := generateApparmorSecurityProfile(test.profile)
			if err != nil {
				if test.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			} else {
				if asp == nil {
					asp = csp
				}
				specOpts, err := generateApparmorSpecOpts(asp, test.privileged, !test.disable)
				assert.Equal(t,
					reflect.ValueOf(test.specOpts).Pointer(),
					reflect.ValueOf(specOpts).Pointer())
				if test.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}

func TestMaskedAndReadonlyPaths(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()

	defaultSpec, err := oci.GenerateSpec(ctrdutil.NamespacedContext(), nil, &containers.Container{ID: testID})
	require.NoError(t, err)

	for _, test := range []struct {
		desc             string
		disableProcMount bool
		masked           []string
		readonly         []string
		expectedMasked   []string
		expectedReadonly []string
		privileged       bool
	}{
		{
			desc:             "should apply default if not specified when disable_proc_mount = true",
			disableProcMount: true,
			masked:           nil,
			readonly:         nil,
			expectedMasked:   defaultSpec.Linux.MaskedPaths,
			expectedReadonly: defaultSpec.Linux.ReadonlyPaths,
			privileged:       false,
		},
		{
			desc:             "should apply default if not specified when disable_proc_mount = false",
			disableProcMount: false,
			masked:           nil,
			readonly:         nil,
			expectedMasked:   []string{},
			expectedReadonly: []string{},
			privileged:       false,
		},
		{
			desc:             "should be able to specify empty paths",
			masked:           []string{},
			readonly:         []string{},
			expectedMasked:   []string{},
			expectedReadonly: []string{},
			privileged:       false,
		},
		{
			desc:             "should apply CRI specified paths",
			masked:           []string{"/proc"},
			readonly:         []string{"/sys"},
			expectedMasked:   []string{"/proc"},
			expectedReadonly: []string{"/sys"},
			privileged:       false,
		},
		{
			desc:             "default should be nil for privileged",
			expectedMasked:   nil,
			expectedReadonly: nil,
			privileged:       true,
		},
		{
			desc:             "should be able to specify empty paths, esp. if privileged",
			masked:           []string{},
			readonly:         []string{},
			expectedMasked:   nil,
			expectedReadonly: nil,
			privileged:       true,
		},
		{
			desc:             "should not apply CRI specified paths if privileged",
			masked:           []string{"/proc"},
			readonly:         []string{"/sys"},
			expectedMasked:   nil,
			expectedReadonly: nil,
			privileged:       true,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c.config.DisableProcMount = test.disableProcMount
			containerConfig.Linux.SecurityContext.MaskedPaths = test.masked
			containerConfig.Linux.SecurityContext.ReadonlyPaths = test.readonly
			containerConfig.Linux.SecurityContext.Privileged = test.privileged
			sandboxConfig.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
				Privileged: test.privileged,
			}
			spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			require.NoError(t, err)
			if !test.privileged { // specCheck presumes an unprivileged container
				specCheck(t, testID, testSandboxID, testPid, spec)
			}
			assert.Equal(t, test.expectedMasked, spec.Linux.MaskedPaths)
			assert.Equal(t, test.expectedReadonly, spec.Linux.ReadonlyPaths)
		})
	}
}

func TestHostname(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	c.os.(*ostesting.FakeOS).HostnameFn = func() (string, error) {
		return "real-hostname", nil
	}
	for _, test := range []struct {
		desc        string
		hostname    string
		networkNs   runtime.NamespaceMode
		expectedEnv string
	}{
		{
			desc:        "should add HOSTNAME=sandbox.Hostname for pod network namespace",
			hostname:    "test-hostname",
			networkNs:   runtime.NamespaceMode_POD,
			expectedEnv: "HOSTNAME=test-hostname",
		},
		{
			desc:        "should add HOSTNAME=sandbox.Hostname for host network namespace",
			hostname:    "test-hostname",
			networkNs:   runtime.NamespaceMode_NODE,
			expectedEnv: "HOSTNAME=test-hostname",
		},
		{
			desc:        "should add HOSTNAME=os.Hostname for host network namespace if sandbox.Hostname is not set",
			hostname:    "",
			networkNs:   runtime.NamespaceMode_NODE,
			expectedEnv: "HOSTNAME=real-hostname",
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			sandboxConfig.Hostname = test.hostname
			sandboxConfig.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{Network: test.networkNs},
			}
			spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			require.NoError(t, err)
			specCheck(t, testID, testSandboxID, testPid, spec)
			assert.Contains(t, spec.Process.Env, test.expectedEnv)
		})
	}
}

func TestDisableCgroup(t *testing.T) {
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	c.config.DisableCgroup = true
	spec, err := c.buildContainerSpec(currentPlatform, "test-id", "sandbox-id", 1234, "", "container-name", testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
	require.NoError(t, err)

	t.Log("resource limit should not be set")
	assert.Nil(t, spec.Linux.Resources.Memory)
	assert.Nil(t, spec.Linux.Resources.CPU)

	t.Log("cgroup path should be empty")
	assert.Empty(t, spec.Linux.CgroupsPath)
}

func TestGenerateUserString(t *testing.T) {
	type testcase struct {
		// the name of the test case
		name string

		u        string
		uid, gid *runtime.Int64Value

		result        string
		expectedError bool
	}
	testcases := []testcase{
		{
			name:   "Empty",
			result: "",
		},
		{
			name:   "Username Only",
			u:      "testuser",
			result: "testuser",
		},
		{
			name:   "Username, UID",
			u:      "testuser",
			uid:    &runtime.Int64Value{Value: 1},
			result: "testuser",
		},
		{
			name:   "Username, UID, GID",
			u:      "testuser",
			uid:    &runtime.Int64Value{Value: 1},
			gid:    &runtime.Int64Value{Value: 10},
			result: "testuser:10",
		},
		{
			name:   "Username, GID",
			u:      "testuser",
			gid:    &runtime.Int64Value{Value: 10},
			result: "testuser:10",
		},
		{
			name:   "UID only",
			uid:    &runtime.Int64Value{Value: 1},
			result: "1",
		},
		{
			name:   "UID, GID",
			uid:    &runtime.Int64Value{Value: 1},
			gid:    &runtime.Int64Value{Value: 10},
			result: "1:10",
		},
		{
			name:          "GID only",
			gid:           &runtime.Int64Value{Value: 10},
			result:        "",
			expectedError: true,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := generateUserString(tc.u, tc.uid, tc.gid)
			if tc.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.result, r)
		})
	}
}

func TestProcessUser(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	testContainer := &containers.Container{ID: "64ddfe361f0099f8d59075398feeb3dcb3863b6851df7b946744755066c03e9d"}
	ctx := context.Background()

	etcPasswd := `
root:x:0:0:root:/root:/bin/sh
alice:x:1000:1000:alice:/home/alice:/bin/sh
` // #nosec G101
	etcGroup := `
root:x:0
alice:x:1000:
additional-group-for-alice:x:11111:alice
additional-group-for-root:x:22222:root
`
	tempRootDir := t.TempDir()
	require.NoError(t,
		os.MkdirAll(filepath.Join(tempRootDir, "etc"), 0755),
	)
	require.NoError(t,
		os.WriteFile(filepath.Join(tempRootDir, "etc", "passwd"), []byte(etcPasswd), 0644),
	)
	require.NoError(t,
		os.WriteFile(filepath.Join(tempRootDir, "etc", "group"), []byte(etcGroup), 0644),
	)

	for _, test := range []struct {
		desc            string
		imageConfigUser string
		securityContext *runtime.LinuxContainerSecurityContext
		expected        runtimespec.User
	}{
		{
			desc: "Only SecurityContext was set, SecurityContext defines User",
			securityContext: &runtime.LinuxContainerSecurityContext{
				RunAsUser:          &runtime.Int64Value{Value: 1000},
				RunAsGroup:         &runtime.Int64Value{Value: 2000},
				SupplementalGroups: []int64{3333},
			},
			expected: runtimespec.User{UID: 1000, GID: 2000, AdditionalGids: []uint32{2000, 3333, 11111}},
		},
		{
			desc:            "Only imageConfig.User was set, imageConfig.User defines User",
			imageConfigUser: "1000",
			securityContext: nil,
			expected:        runtimespec.User{UID: 1000, GID: 1000, AdditionalGids: []uint32{1000, 11111}},
		},
		{
			desc:            "Both SecurityContext and ImageConfig.User was set, SecurityContext defines User",
			imageConfigUser: "0",
			securityContext: &runtime.LinuxContainerSecurityContext{
				RunAsUser:          &runtime.Int64Value{Value: 1000},
				RunAsGroup:         &runtime.Int64Value{Value: 2000},
				SupplementalGroups: []int64{3333},
			},
			expected: runtimespec.User{UID: 1000, GID: 2000, AdditionalGids: []uint32{2000, 3333, 11111}},
		},
		{
			desc:     "No SecurityContext nor ImageConfig.User were set, runtime default defines User",
			expected: runtimespec.User{UID: 0, GID: 0, AdditionalGids: []uint32{0, 22222}},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
			containerConfig.Linux.SecurityContext = test.securityContext
			imageConfig.User = test.imageConfigUser

			spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			require.NoError(t, err)

			spec.Root.Path = tempRootDir // simulating /etc/{passwd, group}
			opts, err := c.platformSpecOpts(platforms.DefaultSpec(), containerConfig, imageConfig)
			require.NoError(t, err)
			oci.ApplyOpts(ctx, nil, testContainer, spec, opts...)

			require.Equal(t, test.expected, spec.Process.User)
		})
	}
}

func TestNonRootUserAndDevices(t *testing.T) {
	testPid := uint32(1234)
	c := newTestCRIService()
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()

	hostDevicesRaw, err := oci.HostDevices()
	assert.NoError(t, err)

	testDevice := hostDevicesRaw[0]

	for _, test := range []struct {
		desc                               string
		uid, gid                           *runtime.Int64Value
		deviceOwnershipFromSecurityContext bool
		expectedDeviceUID                  uint32
		expectedDeviceGID                  uint32
	}{
		{
			desc:              "expect non-root container's Devices Uid/Gid to be the same as the device Uid/Gid on the host when deviceOwnershipFromSecurityContext is disabled",
			uid:               &runtime.Int64Value{Value: 1},
			gid:               &runtime.Int64Value{Value: 10},
			expectedDeviceUID: *testDevice.UID,
			expectedDeviceGID: *testDevice.GID,
		},
		{
			desc:              "expect root container's Devices Uid/Gid to be the same as the device Uid/Gid on the host when deviceOwnershipFromSecurityContext is disabled",
			uid:               &runtime.Int64Value{Value: 0},
			gid:               &runtime.Int64Value{Value: 0},
			expectedDeviceUID: *testDevice.UID,
			expectedDeviceGID: *testDevice.GID,
		},
		{
			desc:                               "expect non-root container's Devices Uid/Gid to be the same as RunAsUser/RunAsGroup when deviceOwnershipFromSecurityContext is enabled",
			uid:                                &runtime.Int64Value{Value: 1},
			gid:                                &runtime.Int64Value{Value: 10},
			deviceOwnershipFromSecurityContext: true,
			expectedDeviceUID:                  1,
			expectedDeviceGID:                  10,
		},
		{
			desc:                               "expect root container's Devices Uid/Gid to be the same as the device Uid/Gid on the host when deviceOwnershipFromSecurityContext is enabled",
			uid:                                &runtime.Int64Value{Value: 0},
			gid:                                &runtime.Int64Value{Value: 0},
			deviceOwnershipFromSecurityContext: true,
			expectedDeviceUID:                  *testDevice.UID,
			expectedDeviceGID:                  *testDevice.GID,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c.config.DeviceOwnershipFromSecurityContext = test.deviceOwnershipFromSecurityContext
			containerConfig.Linux.SecurityContext.RunAsUser = test.uid
			containerConfig.Linux.SecurityContext.RunAsGroup = test.gid
			containerConfig.Devices = []*runtime.Device{
				{
					ContainerPath: testDevice.Path,
					HostPath:      testDevice.Path,
					Permissions:   "r",
				},
			}

			spec, err := c.buildContainerSpec(currentPlatform, t.Name(), testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, config.Runtime{})
			assert.NoError(t, err)

			assert.Equal(t, test.expectedDeviceUID, *spec.Linux.Devices[0].UID)
			assert.Equal(t, test.expectedDeviceGID, *spec.Linux.Devices[0].GID)
		})
	}
}

func TestPrivilegedDevices(t *testing.T) {
	testPid := uint32(1234)
	c := newTestCRIService()
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()

	for _, test := range []struct {
		desc                                          string
		privileged                                    bool
		privilegedWithoutHostDevices                  bool
		privilegedWithoutHostDevicesAllDevicesAllowed bool
		expectHostDevices                             bool
		expectAllDevicesAllowed                       bool
	}{
		{
			desc:                         "expect no host devices when privileged is false",
			privileged:                   false,
			privilegedWithoutHostDevices: false,
			privilegedWithoutHostDevicesAllDevicesAllowed: false,
			expectHostDevices:       false,
			expectAllDevicesAllowed: false,
		},
		{
			desc:                         "expect no host devices when privileged is false and privilegedWithoutHostDevices is true",
			privileged:                   false,
			privilegedWithoutHostDevices: true,
			privilegedWithoutHostDevicesAllDevicesAllowed: false,
			expectHostDevices:       false,
			expectAllDevicesAllowed: false,
		},
		{
			desc:                         "expect host devices and all device allowlist when privileged is true",
			privileged:                   true,
			privilegedWithoutHostDevices: false,
			privilegedWithoutHostDevicesAllDevicesAllowed: false,
			expectHostDevices:       true,
			expectAllDevicesAllowed: true,
		},
		{
			desc:                         "expect no host devices when privileged is true and privilegedWithoutHostDevices is true",
			privileged:                   true,
			privilegedWithoutHostDevices: true,
			privilegedWithoutHostDevicesAllDevicesAllowed: false,
			expectHostDevices:       false,
			expectAllDevicesAllowed: false,
		},
		{
			desc:                         "expect host devices and all devices allowlist when privileged is true and privilegedWithoutHostDevices is true and privilegedWithoutHostDevicesAllDevicesAllowed is true",
			privileged:                   true,
			privilegedWithoutHostDevices: true,
			privilegedWithoutHostDevicesAllDevicesAllowed: true,
			expectHostDevices:       false,
			expectAllDevicesAllowed: true,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			containerConfig.Linux.SecurityContext.Privileged = test.privileged
			sandboxConfig.Linux.SecurityContext.Privileged = test.privileged

			ociRuntime := config.Runtime{
				PrivilegedWithoutHostDevices:                  test.privilegedWithoutHostDevices,
				PrivilegedWithoutHostDevicesAllDevicesAllowed: test.privilegedWithoutHostDevicesAllDevicesAllowed,
			}
			spec, err := c.buildContainerSpec(currentPlatform, t.Name(), testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			assert.NoError(t, err)

			hostDevicesRaw, err := oci.HostDevices()
			assert.NoError(t, err)
			var hostDevices = make([]string, 0)
			for _, dev := range hostDevicesRaw {
				// https://github.com/containerd/cri/pull/1521#issuecomment-652807951
				if dev.Major != 0 {
					hostDevices = append(hostDevices, dev.Path)
				}
			}

			if test.expectHostDevices {
				assert.Len(t, spec.Linux.Devices, len(hostDevices))
			} else {
				assert.Empty(t, spec.Linux.Devices)
			}

			assert.Len(t, spec.Linux.Resources.Devices, 1)
			assert.Equal(t, spec.Linux.Resources.Devices[0].Allow, test.expectAllDevicesAllowed)
			assert.Equal(t, spec.Linux.Resources.Devices[0].Access, "rwm")
		})
	}
}

func TestBaseOCISpec(t *testing.T) {
	c := newTestCRIService()
	baseLimit := int64(100)
	c.baseOCISpecs = map[string]*oci.Spec{
		"/etc/containerd/cri-base.json": {
			Process: &runtimespec.Process{
				User: runtimespec.User{AdditionalGids: []uint32{9999}},
				Capabilities: &runtimespec.LinuxCapabilities{
					Permitted: []string{"CAP_SETUID"},
				},
			},
			Linux: &runtimespec.Linux{
				Resources: &runtimespec.LinuxResources{
					Memory: &runtimespec.LinuxMemory{Limit: &baseLimit}, // Will be overwritten by `getCreateContainerTestData`
				},
			},
		},
	}

	ociRuntime := config.Runtime{}
	ociRuntime.BaseRuntimeSpec = "/etc/containerd/cri-base.json"

	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()

	spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
	assert.NoError(t, err)

	specCheck(t, testID, testSandboxID, testPid, spec)

	assert.Contains(t, spec.Process.User.AdditionalGids, uint32(9999))
	assert.Len(t, spec.Process.User.AdditionalGids, 3)

	assert.Contains(t, spec.Process.Capabilities.Permitted, "CAP_SETUID")
	assert.Len(t, spec.Process.Capabilities.Permitted, 1)

	assert.Equal(t, *spec.Linux.Resources.Memory.Limit, containerConfig.Linux.Resources.MemoryLimitInBytes)
}

func writeFilesToTempDir(tmpDirPattern string, content []string) (string, error) {
	if len(content) == 0 {
		return "", nil
	}

	dir, err := os.MkdirTemp("", tmpDirPattern)
	if err != nil {
		return "", err
	}

	for idx, data := range content {
		file := filepath.Join(dir, fmt.Sprintf("spec-%d.yaml", idx))
		err := os.WriteFile(file, []byte(data), 0644)
		if err != nil {
			return "", err
		}
	}

	return dir, nil
}

func TestCDIInjections(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	testContainer := &containers.Container{ID: "64ddfe361f0099f8d59075398feeb3dcb3863b6851df7b946744755066c03e9d"}
	ctx := context.Background()

	for _, test := range []struct {
		description   string
		cdiSpecFiles  []string
		cdiDevices    []*runtime.CDIDevice
		annotations   map[string]string
		expectError   bool
		expectDevices []runtimespec.LinuxDevice
		expectEnv     []string
	}{
		{description: "expect no CDI error for nil annotations",
			cdiDevices: []*runtime.CDIDevice{},
		},
		{description: "expect no CDI error for nil CDIDevices",
			annotations: map[string]string{},
		},
		{description: "expect no CDI error for empty CDI devices and annotations",
			cdiDevices:  []*runtime.CDIDevice{},
			annotations: map[string]string{},
		},
		{description: "expect CDI error for invalid CDI device reference in annotations",
			annotations: map[string]string{
				cdi.AnnotationPrefix + "devices": "foobar",
			},
			expectError: true,
		},
		{description: "expect CDI error for invalid CDI device reference in CDIDevices",
			cdiDevices: []*runtime.CDIDevice{
				{Name: "foobar"},
			},
			expectError: true,
		},
		{description: "expect CDI error for unresolvable devices in annotations",
			annotations: map[string]string{
				cdi.AnnotationPrefix + "vendor1_devices": "vendor1.com/device=no-such-dev",
			},
			expectError: true,
		},
		{description: "expect CDI error for unresolvable devices in CDIDevices",
			cdiDevices: []*runtime.CDIDevice{
				{Name: "vendor1.com/device=no-such-dev"},
			},
			expectError: true,
		},
		{description: "expect properly injected resolvable CDI devices from annotations",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.3.0"
kind: "vendor1.com/device"
devices:
  - name: foo
    containerEdits:
      deviceNodes:
        - path: /dev/loop8
          type: b
          major: 7
          minor: 8
      env:
        - FOO=injected
containerEdits:
  env:
    - "VENDOR1=present"
`,
				`
cdiVersion: "0.3.0"
kind: "vendor2.com/device"
devices:
  - name: bar
    containerEdits:
      deviceNodes:
        - path: /dev/loop9
          type: b
          major: 7
          minor: 9
      env:
        - BAR=injected
containerEdits:
  env:
    - "VENDOR2=present"
`,
			},
			annotations: map[string]string{
				cdi.AnnotationPrefix + "vendor1_devices": "vendor1.com/device=foo",
				cdi.AnnotationPrefix + "vendor2_devices": "vendor2.com/device=bar",
			},
			expectDevices: []runtimespec.LinuxDevice{
				{
					Path:  "/dev/loop8",
					Type:  "b",
					Major: 7,
					Minor: 8,
				},
				{
					Path:  "/dev/loop9",
					Type:  "b",
					Major: 7,
					Minor: 9,
				},
			},
			expectEnv: []string{
				"FOO=injected",
				"VENDOR1=present",
				"BAR=injected",
				"VENDOR2=present",
			},
		},
		{description: "expect properly injected resolvable CDI devices from CDIDevices",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.3.0"
kind: "vendor1.com/device"
devices:
  - name: foo
    containerEdits:
      deviceNodes:
        - path: /dev/loop8
          type: b
          major: 7
          minor: 8
      env:
        - FOO=injected
containerEdits:
  env:
    - "VENDOR1=present"
`,
				`
cdiVersion: "0.3.0"
kind: "vendor2.com/device"
devices:
  - name: bar
    containerEdits:
      deviceNodes:
        - path: /dev/loop9
          type: b
          major: 7
          minor: 9
      env:
        - BAR=injected
containerEdits:
  env:
    - "VENDOR2=present"
`,
			},
			cdiDevices: []*runtime.CDIDevice{
				{Name: "vendor1.com/device=foo"},
				{Name: "vendor2.com/device=bar"},
			},
			expectDevices: []runtimespec.LinuxDevice{
				{
					Path:  "/dev/loop8",
					Type:  "b",
					Major: 7,
					Minor: 8,
				},
				{
					Path:  "/dev/loop9",
					Type:  "b",
					Major: 7,
					Minor: 9,
				},
			},
			expectEnv: []string{
				"FOO=injected",
				"VENDOR1=present",
				"BAR=injected",
				"VENDOR2=present",
			},
		},
		{description: "expect CDI devices from CDIDevices and annotations",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.3.0"
kind: "vendor1.com/device"
devices:
  - name: foo
    containerEdits:
      deviceNodes:
        - path: /dev/loop8
          type: b
          major: 7
          minor: 8
      env:
        - FOO=injected
containerEdits:
  env:
    - "VENDOR1=present"
`,
				`
cdiVersion: "0.3.0"
kind: "vendor2.com/device"
devices:
  - name: bar
    containerEdits:
      deviceNodes:
        - path: /dev/loop9
          type: b
          major: 7
          minor: 9
      env:
        - BAR=injected
containerEdits:
  env:
    - "VENDOR2=present"
`,
				`
cdiVersion: "0.3.0"
kind: "vendor3.com/device"
devices:
  - name: foo3
    containerEdits:
      deviceNodes:
        - path: /dev/loop10
          type: b
          major: 7
          minor: 10
      env:
        - FOO3=injected
containerEdits:
  env:
    - "VENDOR3=present"
`,
				`
cdiVersion: "0.3.0"
kind: "vendor4.com/device"
devices:
  - name: bar4
    containerEdits:
      deviceNodes:
        - path: /dev/loop11
          type: b
          major: 7
          minor: 11
      env:
        - BAR4=injected
containerEdits:
  env:
    - "VENDOR4=present"
`,
			},
			cdiDevices: []*runtime.CDIDevice{
				{Name: "vendor1.com/device=foo"},
				{Name: "vendor2.com/device=bar"},
				{Name: "vendor3.com/device=foo3"},
			},
			annotations: map[string]string{
				cdi.AnnotationPrefix + "vendor3_devices": "vendor3.com/device=foo3", // Duplicated device, should be ignored
				cdi.AnnotationPrefix + "vendor4_devices": "vendor4.com/device=bar4",
			},
			expectDevices: []runtimespec.LinuxDevice{
				{
					Path:  "/dev/loop8",
					Type:  "b",
					Major: 7,
					Minor: 8,
				},
				{
					Path:  "/dev/loop9",
					Type:  "b",
					Major: 7,
					Minor: 9,
				},
				{
					Path:  "/dev/loop10",
					Type:  "b",
					Major: 7,
					Minor: 10,
				},
				{
					Path:  "/dev/loop11",
					Type:  "b",
					Major: 7,
					Minor: 11,
				},
			},
			expectEnv: []string{
				"FOO=injected",
				"VENDOR1=present",
				"BAR=injected",
				"VENDOR2=present",
				"FOO3=injected",
				"VENDOR3=present",
				"BAR4=injected",
				"VENDOR4=present",
			},
		},
	} {
		t.Run(test.description, func(t *testing.T) {
			spec, err := c.buildContainerSpec(currentPlatform, testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			require.NoError(t, err)

			specCheck(t, testID, testSandboxID, testPid, spec)

			cdiDir, err := writeFilesToTempDir("containerd-test-CDI-injections-", test.cdiSpecFiles)
			if cdiDir != "" {
				defer os.RemoveAll(cdiDir)
			}
			require.NoError(t, err)

			reg := cdi.GetRegistry()
			err = reg.Configure(cdi.WithSpecDirs(cdiDir))
			require.NoError(t, err)

			injectFun := customopts.WithCDI(test.annotations, test.cdiDevices)
			err = injectFun(ctx, nil, testContainer, spec)
			assert.Equal(t, test.expectError, err != nil)

			if err != nil {
				if test.expectEnv != nil {
					for _, expectedEnv := range test.expectEnv {
						assert.Contains(t, spec.Process.Env, expectedEnv)
					}
				}
				if test.expectDevices != nil {
					for _, expectedDev := range test.expectDevices {
						assert.Contains(t, spec.Linux.Devices, expectedDev)
					}
				}
			}
		})
	}
}
