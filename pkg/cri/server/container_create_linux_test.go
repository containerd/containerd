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

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/contrib/apparmor"
	"github.com/containerd/containerd/contrib/seccomp"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/oci"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/pkg/cap"
	"github.com/containerd/containerd/pkg/cri/annotations"
	"github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/opts"
	"github.com/containerd/containerd/pkg/cri/util"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	ostesting "github.com/containerd/containerd/pkg/os/testing"
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
			includes: allCaps,
		},
		"should be able to drop all capabilities": {
			capability: &runtime.Capability{
				DropCapabilities: []string{"ALL"},
			},
			excludes: allCaps,
		},
		"should be able to drop capabilities with add all": {
			capability: &runtime.Capability{
				AddCapabilities:  []string{"ALL"},
				DropCapabilities: []string{"CHOWN"},
			},
			includes: util.SubtractStringSlice(allCaps, "CAP_CHOWN"),
			excludes: []string{"CAP_CHOWN"},
		},
		"should be able to add capabilities with drop all": {
			capability: &runtime.Capability{
				AddCapabilities:  []string{"SYS_ADMIN"},
				DropCapabilities: []string{"ALL"},
			},
			includes: []string{"CAP_SYS_ADMIN"},
			excludes: util.SubtractStringSlice(allCaps, "CAP_SYS_ADMIN"),
		},
	} {
		t.Logf("TestCase %q", desc)
		containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
		ociRuntime := config.Runtime{}
		c := newTestCRIService()
		c.allCaps = allCaps

		containerConfig.Linux.SecurityContext.Capabilities = test.capability
		spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
		require.NoError(t, err)

		if selinux.GetEnabled() {
			assert.NotEqual(t, "", spec.Process.SelinuxLabel)
			assert.NotEqual(t, "", spec.Linux.MountLabel)
		}

		specCheck(t, testID, testSandboxID, testPid, spec)
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
		assert.Empty(t, spec.Process.Capabilities.Ambient)
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
		spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
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
		spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
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
		spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
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
		{
			ContainerPath: "/dev",
			HostPath:      "test-dev-extra",
			Readonly:      false,
		},
	}
	spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, extraMounts, ociRuntime)
	require.NoError(t, err)
	specCheck(t, testID, testSandboxID, testPid, spec)
	var mounts, sysMounts, devMounts []runtimespec.Mount
	for _, m := range spec.Mounts {
		if strings.HasPrefix(m.Destination, "test-container-path") {
			mounts = append(mounts, m)
		} else if m.Destination == "/sys" {
			sysMounts = append(sysMounts, m)
		} else if strings.HasPrefix(m.Destination, "/dev") {
			devMounts = append(devMounts, m)
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

	t.Logf("Dev mount should override all default dev mounts")
	require.Len(t, devMounts, 1)
	assert.Equal(t, "test-dev-extra", devMounts[0].Source)
	assert.Contains(t, devMounts[0].Options, "rw")
}

func TestContainerAndSandboxPrivileged(t *testing.T) {
	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	for desc, test := range map[string]struct {
		containerPrivileged bool
		sandboxPrivileged   bool
		expectError         bool
	}{
		"privileged container in non-privileged sandbox should fail": {
			containerPrivileged: true,
			sandboxPrivileged:   false,
			expectError:         true,
		},
		"privileged container in privileged sandbox should be fine": {
			containerPrivileged: true,
			sandboxPrivileged:   true,
			expectError:         false,
		},
		"non-privileged container in privileged sandbox should be fine": {
			containerPrivileged: false,
			sandboxPrivileged:   true,
			expectError:         false,
		},
		"non-privileged container in non-privileged sandbox should be fine": {
			containerPrivileged: false,
			sandboxPrivileged:   false,
			expectError:         false,
		},
	} {
		t.Logf("TestCase %q", desc)
		containerConfig.Linux.SecurityContext.Privileged = test.containerPrivileged
		sandboxConfig.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
			Privileged: test.sandboxPrivileged,
		}
		_, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
		if test.expectError {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestContainerMounts(t *testing.T) {
	const testSandboxID = "test-id"
	for desc, test := range map[string]struct {
		statFn          func(string) (os.FileInfo, error)
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
					ContainerPath: "/etc/hostname",
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "hostname"),
					Readonly:      true,
				},
				{
					ContainerPath: "/etc/hosts",
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "hosts"),
					Readonly:      true,
				},
				{
					ContainerPath: resolvConfPath,
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "resolv.conf"),
					Readonly:      true,
				},
				{
					ContainerPath:  "/dev/shm",
					HostPath:       filepath.Join(testStateDir, sandboxesDir, testSandboxID, "shm"),
					Readonly:       false,
					SelinuxRelabel: true,
				},
			},
		},
		"should setup rw mount when rootfs is read-write": {
			securityContext: &runtime.LinuxContainerSecurityContext{},
			expectedMounts: []*runtime.Mount{
				{
					ContainerPath: "/etc/hostname",
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "hostname"),
					Readonly:      false,
				},
				{
					ContainerPath: "/etc/hosts",
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "hosts"),
					Readonly:      false,
				},
				{
					ContainerPath: resolvConfPath,
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "resolv.conf"),
					Readonly:      false,
				},
				{
					ContainerPath:  "/dev/shm",
					HostPath:       filepath.Join(testStateDir, sandboxesDir, testSandboxID, "shm"),
					Readonly:       false,
					SelinuxRelabel: true,
				},
			},
		},
		"should use host /dev/shm when host ipc is set": {
			securityContext: &runtime.LinuxContainerSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{Ipc: runtime.NamespaceMode_NODE},
			},
			expectedMounts: []*runtime.Mount{
				{
					ContainerPath: "/etc/hostname",
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "hostname"),
					Readonly:      false,
				},
				{
					ContainerPath: "/etc/hosts",
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "hosts"),
					Readonly:      false,
				},
				{
					ContainerPath: resolvConfPath,
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "resolv.conf"),
					Readonly:      false,
				},
				{
					ContainerPath: "/dev/shm",
					HostPath:      "/dev/shm",
					Readonly:      false,
				},
			},
		},
		"should skip container mounts if already mounted by CRI": {
			criMounts: []*runtime.Mount{
				{
					ContainerPath: "/etc/hostname",
					HostPath:      "/test-etc-hostname",
				},
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
		"should skip hostname mount if the old sandbox doesn't have hostname file": {
			statFn: func(path string) (os.FileInfo, error) {
				assert.Equal(t, filepath.Join(testRootDir, sandboxesDir, testSandboxID, "hostname"), path)
				return nil, errors.New("random error")
			},
			securityContext: &runtime.LinuxContainerSecurityContext{},
			expectedMounts: []*runtime.Mount{
				{
					ContainerPath: "/etc/hosts",
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "hosts"),
					Readonly:      false,
				},
				{
					ContainerPath: resolvConfPath,
					HostPath:      filepath.Join(testRootDir, sandboxesDir, testSandboxID, "resolv.conf"),
					Readonly:      false,
				},
				{
					ContainerPath:  "/dev/shm",
					HostPath:       filepath.Join(testStateDir, sandboxesDir, testSandboxID, "shm"),
					Readonly:       false,
					SelinuxRelabel: true,
				},
			},
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
		c := newTestCRIService()
		c.os.(*ostesting.FakeOS).StatFn = test.statFn
		mounts := c.containerMounts(testSandboxID, config)
		assert.Equal(t, test.expectedMounts, mounts, desc)
	}
}

func TestPrivilegedBindMount(t *testing.T) {
	testPid := uint32(1234)
	c := newTestCRIService()
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}

	for desc, test := range map[string]struct {
		privileged         bool
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
	} {
		t.Logf("TestCase %q", desc)

		containerConfig.Linux.SecurityContext.Privileged = test.privileged
		sandboxConfig.Linux.SecurityContext.Privileged = test.privileged

		spec, err := c.containerSpec(t.Name(), testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)

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
	for desc, test := range map[string]struct {
		pidNS    runtime.NamespaceMode
		expected runtimespec.LinuxNamespace
	}{
		"node namespace mode": {
			pidNS: runtime.NamespaceMode_NODE,
			expected: runtimespec.LinuxNamespace{
				Type: runtimespec.PIDNamespace,
				Path: opts.GetPIDNamespace(testPid),
			},
		},
		"container namespace mode": {
			pidNS: runtime.NamespaceMode_CONTAINER,
			expected: runtimespec.LinuxNamespace{
				Type: runtimespec.PIDNamespace,
			},
		},
		"pod namespace mode": {
			pidNS: runtime.NamespaceMode_POD,
			expected: runtimespec.LinuxNamespace{
				Type: runtimespec.PIDNamespace,
				Path: opts.GetPIDNamespace(testPid),
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		containerConfig.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{Pid: test.pidNS}
		spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
		require.NoError(t, err)
		assert.Contains(t, spec.Linux.Namespaces, test.expected)
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

	spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
	assert.NoError(t, err)
	for _, mount := range spec.Mounts {
		assert.NotEqual(t, "/run", mount.Destination)
	}
}

func TestGenerateSeccompSecurityProfileSpecOpts(t *testing.T) {
	for desc, test := range map[string]struct {
		profile        string
		privileged     bool
		disable        bool
		specOpts       oci.SpecOpts
		expectErr      bool
		defaultProfile string
		sp             *runtime.SecurityProfile
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
		"should use default profile when seccomp is empty": {
			defaultProfile: profileNamePrefix + "test-profile",
			specOpts:       seccomp.WithProfile("test-profile"),
		},
		"should fallback to docker/default when seccomp is empty and default is runtime/default": {
			defaultProfile: runtimeDefault,
			specOpts:       seccomp.WithDefaultProfile(),
		},
		//-----------------------------------------------
		// now buckets for the SecurityProfile variants
		//-----------------------------------------------
		"sp should return error if seccomp is specified when seccomp is not supported": {
			disable:   true,
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		"sp should not return error if seccomp is unconfined when seccomp is not supported": {
			disable: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		"sp should not set seccomp when privileged is true": {
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		"sp should not set seccomp when seccomp is unconfined": {
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		"sp should not set seccomp when seccomp is not specified": {},
		"sp should set default seccomp when seccomp is runtime/default": {
			specOpts: seccomp.WithDefaultProfile(),
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		"sp should set specified profile when local profile is specified": {
			specOpts: seccomp.WithProfile("test-profile"),
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
		"sp should set specified profile when local profile is specified even without prefix": {
			specOpts: seccomp.WithProfile("test-profile"),
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: "test-profile",
			},
		},
		"sp should return error if specified profile is invalid": {
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_RuntimeDefault,
				LocalhostRef: "test-profile",
			},
		},
	} {
		t.Run(fmt.Sprintf("TestCase %q", desc), func(t *testing.T) {
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
	for desc, test := range map[string]struct {
		profile    string
		privileged bool
		disable    bool
		specOpts   oci.SpecOpts
		expectErr  bool
		sp         *runtime.SecurityProfile
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
		"should not apparmor when apparmor is default and privileged is true": {
			profile:    runtimeDefault,
			privileged: true,
		},
		// TODO (mikebrow) add success with existing defined profile tests
		"should return error when undefined local profile is specified": {
			profile:   profileNamePrefix + "test-profile",
			expectErr: true,
		},
		"should return error when undefined local profile is specified and privileged is true": {
			profile:    profileNamePrefix + "test-profile",
			privileged: true,
			expectErr:  true,
		},
		"should return error if specified profile is invalid": {
			profile:   "test-profile",
			expectErr: true,
		},
		//--------------------------------------
		// buckets for SecurityProfile struct
		//--------------------------------------
		"sp should return error if apparmor is specified when apparmor is not supported": {
			disable:   true,
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		"sp should not return error if apparmor is unconfined when apparmor is not supported": {
			disable: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		"sp should not apparmor when apparmor is unconfined": {
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		"sp should not apparmor when apparmor is unconfined and privileged is true": {
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		"sp should set default apparmor when apparmor is runtime/default": {
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		"sp should not apparmor when apparmor is default and privileged is true": {
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		"sp should return error when undefined local profile is specified": {
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
		"sp should return error when undefined local profile is specified even without prefix": {
			profile:   profileNamePrefix + "test-profile",
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: "test-profile",
			},
		},
		"sp should return error when undefined local profile is specified and privileged is true": {
			privileged: true,
			expectErr:  true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
	} {
		t.Logf("TestCase %q", desc)
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

	for desc, test := range map[string]struct {
		disableProcMount bool
		masked           []string
		readonly         []string
		expectedMasked   []string
		expectedReadonly []string
		privileged       bool
	}{
		"should apply default if not specified when disable_proc_mount = true": {
			disableProcMount: true,
			masked:           nil,
			readonly:         nil,
			expectedMasked:   defaultSpec.Linux.MaskedPaths,
			expectedReadonly: defaultSpec.Linux.ReadonlyPaths,
			privileged:       false,
		},
		"should apply default if not specified when disable_proc_mount = false": {
			disableProcMount: false,
			masked:           nil,
			readonly:         nil,
			expectedMasked:   []string{},
			expectedReadonly: []string{},
			privileged:       false,
		},
		"should be able to specify empty paths": {
			masked:           []string{},
			readonly:         []string{},
			expectedMasked:   []string{},
			expectedReadonly: []string{},
			privileged:       false,
		},
		"should apply CRI specified paths": {
			masked:           []string{"/proc"},
			readonly:         []string{"/sys"},
			expectedMasked:   []string{"/proc"},
			expectedReadonly: []string{"/sys"},
			privileged:       false,
		},
		"default should be nil for privileged": {
			expectedMasked:   nil,
			expectedReadonly: nil,
			privileged:       true,
		},
		"should be able to specify empty paths, esp. if privileged": {
			masked:           []string{},
			readonly:         []string{},
			expectedMasked:   nil,
			expectedReadonly: nil,
			privileged:       true,
		},
		"should not apply CRI specified paths if privileged": {
			masked:           []string{"/proc"},
			readonly:         []string{"/sys"},
			expectedMasked:   nil,
			expectedReadonly: nil,
			privileged:       true,
		},
	} {
		t.Logf("TestCase %q", desc)
		c.config.DisableProcMount = test.disableProcMount
		containerConfig.Linux.SecurityContext.MaskedPaths = test.masked
		containerConfig.Linux.SecurityContext.ReadonlyPaths = test.readonly
		containerConfig.Linux.SecurityContext.Privileged = test.privileged
		sandboxConfig.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
			Privileged: test.privileged,
		}
		spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
		require.NoError(t, err)
		if !test.privileged { // specCheck presumes an unprivileged container
			specCheck(t, testID, testSandboxID, testPid, spec)
		}
		assert.Equal(t, test.expectedMasked, spec.Linux.MaskedPaths)
		assert.Equal(t, test.expectedReadonly, spec.Linux.ReadonlyPaths)
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
	for desc, test := range map[string]struct {
		hostname    string
		networkNs   runtime.NamespaceMode
		expectedEnv string
	}{
		"should add HOSTNAME=sandbox.Hostname for pod network namespace": {
			hostname:    "test-hostname",
			networkNs:   runtime.NamespaceMode_POD,
			expectedEnv: "HOSTNAME=test-hostname",
		},
		"should add HOSTNAME=sandbox.Hostname for host network namespace": {
			hostname:    "test-hostname",
			networkNs:   runtime.NamespaceMode_NODE,
			expectedEnv: "HOSTNAME=test-hostname",
		},
		"should add HOSTNAME=os.Hostname for host network namespace if sandbox.Hostname is not set": {
			hostname:    "",
			networkNs:   runtime.NamespaceMode_NODE,
			expectedEnv: "HOSTNAME=real-hostname",
		},
	} {
		t.Logf("TestCase %q", desc)
		sandboxConfig.Hostname = test.hostname
		sandboxConfig.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
			NamespaceOptions: &runtime.NamespaceOption{Network: test.networkNs},
		}
		spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
		require.NoError(t, err)
		specCheck(t, testID, testSandboxID, testPid, spec)
		assert.Contains(t, spec.Process.Env, test.expectedEnv)
	}
}

func TestDisableCgroup(t *testing.T) {
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	c.config.DisableCgroup = true
	spec, err := c.containerSpec("test-id", "sandbox-id", 1234, "", "container-name", testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
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

func TestNonRootUserAndDevices(t *testing.T) {
	testPid := uint32(1234)
	c := newTestCRIService()
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()

	hostDevicesRaw, err := oci.HostDevices()
	assert.NoError(t, err)

	testDevice := hostDevicesRaw[0]

	for desc, test := range map[string]struct {
		uid, gid                           *runtime.Int64Value
		deviceOwnershipFromSecurityContext bool
		expectedDeviceUID                  uint32
		expectedDeviceGID                  uint32
	}{
		"expect non-root container's Devices Uid/Gid to be the same as the device Uid/Gid on the host when deviceOwnershipFromSecurityContext is disabled": {
			uid:               &runtime.Int64Value{Value: 1},
			gid:               &runtime.Int64Value{Value: 10},
			expectedDeviceUID: *testDevice.UID,
			expectedDeviceGID: *testDevice.GID,
		},
		"expect root container's Devices Uid/Gid to be the same as the device Uid/Gid on the host when deviceOwnershipFromSecurityContext is disabled": {
			uid:               &runtime.Int64Value{Value: 0},
			gid:               &runtime.Int64Value{Value: 0},
			expectedDeviceUID: *testDevice.UID,
			expectedDeviceGID: *testDevice.GID,
		},
		"expect non-root container's Devices Uid/Gid to be the same as RunAsUser/RunAsGroup when deviceOwnershipFromSecurityContext is enabled": {
			uid:                                &runtime.Int64Value{Value: 1},
			gid:                                &runtime.Int64Value{Value: 10},
			deviceOwnershipFromSecurityContext: true,
			expectedDeviceUID:                  1,
			expectedDeviceGID:                  10,
		},
		"expect root container's Devices Uid/Gid to be the same as the device Uid/Gid on the host when deviceOwnershipFromSecurityContext is enabled": {
			uid:                                &runtime.Int64Value{Value: 0},
			gid:                                &runtime.Int64Value{Value: 0},
			deviceOwnershipFromSecurityContext: true,
			expectedDeviceUID:                  *testDevice.UID,
			expectedDeviceGID:                  *testDevice.GID,
		},
	} {
		t.Logf("TestCase %q", desc)

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

		spec, err := c.containerSpec(t.Name(), testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, config.Runtime{})
		assert.NoError(t, err)

		assert.Equal(t, test.expectedDeviceUID, *spec.Linux.Devices[0].UID)
		assert.Equal(t, test.expectedDeviceGID, *spec.Linux.Devices[0].GID)
	}
}

func TestPrivilegedDevices(t *testing.T) {
	testPid := uint32(1234)
	c := newTestCRIService()
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	containerConfig, sandboxConfig, imageConfig, _ := getCreateContainerTestData()

	for desc, test := range map[string]struct {
		privileged                   bool
		privilegedWithoutHostDevices bool
		expectHostDevices            bool
	}{
		"expect no host devices when privileged is false": {
			privileged:                   false,
			privilegedWithoutHostDevices: false,
			expectHostDevices:            false,
		},
		"expect no host devices when privileged is false and privilegedWithoutHostDevices is true": {
			privileged:                   false,
			privilegedWithoutHostDevices: true,
			expectHostDevices:            false,
		},
		"expect host devices when privileged is true": {
			privileged:                   true,
			privilegedWithoutHostDevices: false,
			expectHostDevices:            true,
		},
		"expect no host devices when privileged is true and privilegedWithoutHostDevices is true": {
			privileged:                   true,
			privilegedWithoutHostDevices: true,
			expectHostDevices:            false,
		},
	} {
		t.Logf("TestCase %q", desc)

		containerConfig.Linux.SecurityContext.Privileged = test.privileged
		sandboxConfig.Linux.SecurityContext.Privileged = test.privileged

		ociRuntime := config.Runtime{
			PrivilegedWithoutHostDevices: test.privilegedWithoutHostDevices,
		}
		spec, err := c.containerSpec(t.Name(), testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
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

	spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
	assert.NoError(t, err)

	specCheck(t, testID, testSandboxID, testPid, spec)

	assert.Contains(t, spec.Process.User.AdditionalGids, uint32(9999))
	assert.Len(t, spec.Process.User.AdditionalGids, 3)

	assert.Contains(t, spec.Process.Capabilities.Permitted, "CAP_SETUID")
	assert.Len(t, spec.Process.Capabilities.Permitted, 1)

	assert.Equal(t, *spec.Linux.Resources.Memory.Limit, containerConfig.Linux.Resources.MemoryLimitInBytes)
}
