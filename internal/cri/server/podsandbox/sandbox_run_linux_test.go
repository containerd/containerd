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
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/moby/sys/userns"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/annotations"
	"github.com/containerd/containerd/v2/internal/cri/opts"
	ostesting "github.com/containerd/containerd/v2/pkg/os/testing"
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
		Linux: &runtime.LinuxPodSandboxConfig{
			CgroupParent: "/test/cgroup/parent",
		},
	}
	imageConfig := &imagespec.ImageConfig{
		Env:        []string{"a=b", "c=d"},
		Entrypoint: []string{"/pause"},
		Cmd:        []string{"forever"},
		WorkingDir: "/workspace",
	}
	specCheck := func(t *testing.T, id string, spec *runtimespec.Spec) {
		assert.Equal(t, "test-hostname", spec.Hostname)
		assert.Equal(t, getCgroupsPath("/test/cgroup/parent", id), spec.Linux.CgroupsPath)
		assert.Equal(t, relativeRootfsPath, spec.Root.Path)
		assert.Equal(t, true, spec.Root.Readonly)
		assert.Contains(t, spec.Process.Env, "a=b", "c=d")
		assert.Equal(t, []string{"/pause", "forever"}, spec.Process.Args)
		assert.Equal(t, "/workspace", spec.Process.Cwd)
		assert.EqualValues(t, *spec.Linux.Resources.CPU.Shares, opts.DefaultSandboxCPUshares)
		assert.EqualValues(t, *spec.Process.OOMScoreAdj, defaultSandboxOOMAdj)

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

		if selinux.GetEnabled() {
			assert.NotEqual(t, "", spec.Process.SelinuxLabel)
			assert.NotEqual(t, "", spec.Linux.MountLabel)
		}

		assert.Contains(t, spec.Mounts, runtimespec.Mount{
			Source:      "/test/root/sandboxes/test-id/resolv.conf",
			Destination: resolvConfPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro", "nosuid", "nodev", "noexec"},
		})

	}
	return config, imageConfig, specCheck
}

func TestLinuxSandboxContainerSpec(t *testing.T) {
	testID := "test-id"
	nsPath := "test-cni"
	idMap := runtime.IDMapping{
		HostId:      1000,
		ContainerId: 1000,
		Length:      10,
	}
	expIDMap := runtimespec.LinuxIDMapping{
		HostID:      1000,
		ContainerID: 1000,
		Size:        10,
	}

	for _, test := range []struct {
		desc         string
		configChange func(*runtime.PodSandboxConfig)
		specCheck    func(*testing.T, *runtimespec.Spec)
		expectErr    bool
	}{
		{
			desc: "spec should reflect original config",
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				// runtime spec should have expected namespaces enabled by default.
				require.NotNil(t, spec.Linux)
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.NetworkNamespace,
					Path: nsPath,
				})
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.UTSNamespace,
				})
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.PIDNamespace,
				})
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.IPCNamespace,
				})
				assert.Contains(t, spec.Linux.Sysctl["net.ipv4.ip_unprivileged_port_start"], "0")
				if !userns.RunningInUserNS() {
					assert.Contains(t, spec.Linux.Sysctl["net.ipv4.ping_group_range"], "0 2147483647")
				}

			},
		},
		{
			desc: "spec shouldn't have ping_group_range if userns are in use",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						UsernsOptions: &runtime.UserNamespace{
							Mode: runtime.NamespaceMode_POD,
							Uids: []*runtime.IDMapping{&idMap},
							Gids: []*runtime.IDMapping{&idMap},
						},
					},
				}
			},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				require.NotNil(t, spec.Linux)
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.UserNamespace,
				})
				assert.NotContains(t, spec.Linux.Sysctl["net.ipv4.ping_group_range"], "0 2147483647")
			},
		},
		{
			desc: "host namespace",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						Network: runtime.NamespaceMode_NODE,
						Pid:     runtime.NamespaceMode_NODE,
						Ipc:     runtime.NamespaceMode_NODE,
					},
				}
			},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				// runtime spec should disable expected namespaces in host mode.
				require.NotNil(t, spec.Linux)
				assert.NotContains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.NetworkNamespace,
				})
				assert.NotContains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.UTSNamespace,
				})
				assert.NotContains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.PIDNamespace,
				})
				assert.NotContains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.IPCNamespace,
				})
				assert.NotContains(t, spec.Linux.Sysctl["net.ipv4.ip_unprivileged_port_start"], "0")
				assert.NotContains(t, spec.Linux.Sysctl["net.ipv4.ping_group_range"], "0 2147483647")
			},
		},
		{
			desc: "user namespace",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						UsernsOptions: &runtime.UserNamespace{
							Mode: runtime.NamespaceMode_POD,
							Uids: []*runtime.IDMapping{&idMap},
							Gids: []*runtime.IDMapping{&idMap},
						},
					},
				}
			},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				require.NotNil(t, spec.Linux)
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.UserNamespace,
				})
				require.Equal(t, spec.Linux.UIDMappings, []runtimespec.LinuxIDMapping{expIDMap})
				require.Equal(t, spec.Linux.GIDMappings, []runtimespec.LinuxIDMapping{expIDMap})

			},
		},
		{
			desc: "user namespace mode node and mappings",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						UsernsOptions: &runtime.UserNamespace{
							Mode: runtime.NamespaceMode_NODE,
							Uids: []*runtime.IDMapping{&idMap},
							Gids: []*runtime.IDMapping{&idMap},
						},
					},
				}
			},
			expectErr: true,
		},
		{
			desc: "user namespace with several mappings",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						UsernsOptions: &runtime.UserNamespace{
							Mode: runtime.NamespaceMode_NODE,
							Uids: []*runtime.IDMapping{&idMap, &idMap},
							Gids: []*runtime.IDMapping{&idMap, &idMap},
						},
					},
				}
			},
			expectErr: true,
		},
		{
			desc: "user namespace with uneven mappings",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						UsernsOptions: &runtime.UserNamespace{
							Mode: runtime.NamespaceMode_NODE,
							Uids: []*runtime.IDMapping{&idMap, &idMap},
							Gids: []*runtime.IDMapping{&idMap},
						},
					},
				}
			},
			expectErr: true,
		},
		{
			desc: "user namespace mode container",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						UsernsOptions: &runtime.UserNamespace{
							Mode: runtime.NamespaceMode_CONTAINER,
						},
					},
				}
			},
			expectErr: true,
		},
		{
			desc: "user namespace mode target",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						UsernsOptions: &runtime.UserNamespace{
							Mode: runtime.NamespaceMode_TARGET,
						},
					},
				}
			},
			expectErr: true,
		},
		{
			desc: "user namespace unknown mode",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						UsernsOptions: &runtime.UserNamespace{
							Mode: runtime.NamespaceMode(100),
						},
					},
				}
			},
			expectErr: true,
		},
		{
			desc: "should set supplemental groups correctly",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					SupplementalGroups: []int64{1111, 2222},
				}
			},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				require.NotNil(t, spec.Process)
				assert.Contains(t, spec.Process.User.AdditionalGids, uint32(1111))
				assert.Contains(t, spec.Process.User.AdditionalGids, uint32(2222))
			},
		},
		{
			desc: "should overwrite default sysctls",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.Sysctls = map[string]string{
					"net.ipv4.ip_unprivileged_port_start": "500",
					"net.ipv4.ping_group_range":           "1 1000",
				}
			},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				require.NotNil(t, spec.Process)
				assert.Contains(t, spec.Linux.Sysctl["net.ipv4.ip_unprivileged_port_start"], "500")
				assert.Contains(t, spec.Linux.Sysctl["net.ipv4.ping_group_range"], "1 1000")
			},
		},
		{
			desc: "sandbox sizing annotations should be set if LinuxContainerResources were provided",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.Resources = &v1.LinuxContainerResources{
					CpuPeriod:          100,
					CpuQuota:           200,
					CpuShares:          5000,
					MemoryLimitInBytes: 1024,
				}
			},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				value, ok := spec.Annotations[annotations.SandboxCPUPeriod]
				assert.True(t, ok)
				assert.EqualValues(t, strconv.FormatInt(100, 10), value)
				assert.EqualValues(t, "100", value)

				value, ok = spec.Annotations[annotations.SandboxCPUQuota]
				assert.True(t, ok)
				assert.EqualValues(t, "200", value)

				value, ok = spec.Annotations[annotations.SandboxCPUShares]
				assert.True(t, ok)
				assert.EqualValues(t, "5000", value)

				value, ok = spec.Annotations[annotations.SandboxMem]
				assert.True(t, ok)
				assert.EqualValues(t, "1024", value)
			},
		},
		{
			desc: "sandbox sizing annotations should not be set if LinuxContainerResources were not provided",
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				_, ok := spec.Annotations[annotations.SandboxCPUPeriod]
				assert.False(t, ok)
				_, ok = spec.Annotations[annotations.SandboxCPUQuota]
				assert.False(t, ok)
				_, ok = spec.Annotations[annotations.SandboxCPUShares]
				assert.False(t, ok)
				_, ok = spec.Annotations[annotations.SandboxMem]
				assert.False(t, ok)
			},
		},
		{
			desc: "sandbox sizing annotations are zero if the resources are set to 0",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.Resources = &v1.LinuxContainerResources{}
			},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				value, ok := spec.Annotations[annotations.SandboxCPUPeriod]
				assert.True(t, ok)
				assert.EqualValues(t, "0", value)
				value, ok = spec.Annotations[annotations.SandboxCPUQuota]
				assert.True(t, ok)
				assert.EqualValues(t, "0", value)
				value, ok = spec.Annotations[annotations.SandboxCPUShares]
				assert.True(t, ok)
				assert.EqualValues(t, "0", value)
				value, ok = spec.Annotations[annotations.SandboxMem]
				assert.True(t, ok)
				assert.EqualValues(t, "0", value)
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c := newControllerService()
			c.config.EnableUnprivilegedICMP = true
			c.config.EnableUnprivilegedPorts = true
			config, imageConfig, specCheck := getRunPodSandboxTestData()
			if test.configChange != nil {
				test.configChange(config)
			}
			spec, err := c.sandboxContainerSpec(testID, config, imageConfig, nsPath, nil)
			if test.expectErr {
				assert.Error(t, err)
				assert.Nil(t, spec)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, spec)
			specCheck(t, testID, spec)
			if test.specCheck != nil {
				test.specCheck(t, spec)
			}
		})
	}
}

func TestSetupSandboxFiles(t *testing.T) {
	const (
		testID       = "test-id"
		realhostname = "test-real-hostname"
	)
	for _, test := range []struct {
		desc          string
		dnsConfig     *runtime.DNSConfig
		hostname      string
		ipcMode       runtime.NamespaceMode
		expectedCalls []ostesting.CalledDetail
	}{
		{
			desc:    "should check host /dev/shm existence when ipc mode is NODE",
			ipcMode: runtime.NamespaceMode_NODE,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "Hostname",
				},
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						filepath.Join(testRootDir, sandboxesDir, testID, "hostname"),
						[]byte(realhostname + "\n"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts",
						filepath.Join(testRootDir, sandboxesDir, testID, "hosts"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/resolv.conf",
						filepath.Join(testRootDir, sandboxesDir, testID, "resolv.conf"),
						os.FileMode(0644),
					},
				},
				{
					Name:      "Stat",
					Arguments: []interface{}{"/dev/shm"},
				},
			},
		},
		{
			desc: "should create new /etc/resolv.conf if DNSOptions is set",
			dnsConfig: &runtime.DNSConfig{
				Servers:  []string{"8.8.8.8"},
				Searches: []string{"114.114.114.114"},
				Options:  []string{"timeout:1"},
			},
			ipcMode: runtime.NamespaceMode_NODE,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "Hostname",
				},
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						filepath.Join(testRootDir, sandboxesDir, testID, "hostname"),
						[]byte(realhostname + "\n"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts",
						filepath.Join(testRootDir, sandboxesDir, testID, "hosts"),
						os.FileMode(0644),
					},
				},
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						filepath.Join(testRootDir, sandboxesDir, testID, "resolv.conf"),
						[]byte(`search 114.114.114.114
nameserver 8.8.8.8
options timeout:1
`), os.FileMode(0644),
					},
				},
				{
					Name:      "Stat",
					Arguments: []interface{}{"/dev/shm"},
				},
			},
		},
		{
			desc:      "should create empty /etc/resolv.conf if DNSOptions is empty",
			dnsConfig: &runtime.DNSConfig{},
			ipcMode:   runtime.NamespaceMode_NODE,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "Hostname",
				},
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						filepath.Join(testRootDir, sandboxesDir, testID, "hostname"),
						[]byte(realhostname + "\n"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts",
						filepath.Join(testRootDir, sandboxesDir, testID, "hosts"),
						os.FileMode(0644),
					},
				},
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						filepath.Join(testRootDir, sandboxesDir, testID, "resolv.conf"),
						[]byte{},
						os.FileMode(0644),
					},
				},
				{
					Name:      "Stat",
					Arguments: []interface{}{"/dev/shm"},
				},
			},
		},
		{
			desc:      "should copy host /etc/resolv.conf if DNSOptions is not set",
			dnsConfig: nil,
			ipcMode:   runtime.NamespaceMode_NODE,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "Hostname",
				},
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						filepath.Join(testRootDir, sandboxesDir, testID, "hostname"),
						[]byte(realhostname + "\n"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts",
						filepath.Join(testRootDir, sandboxesDir, testID, "hosts"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						filepath.Join("/etc/resolv.conf"),
						filepath.Join(testRootDir, sandboxesDir, testID, "resolv.conf"),
						os.FileMode(0644),
					},
				},
				{
					Name:      "Stat",
					Arguments: []interface{}{"/dev/shm"},
				},
			},
		},
		{
			desc:    "should create sandbox shm when ipc namespace mode is not NODE",
			ipcMode: runtime.NamespaceMode_POD,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "Hostname",
				},
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						filepath.Join(testRootDir, sandboxesDir, testID, "hostname"),
						[]byte(realhostname + "\n"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts",
						filepath.Join(testRootDir, sandboxesDir, testID, "hosts"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/resolv.conf",
						filepath.Join(testRootDir, sandboxesDir, testID, "resolv.conf"),
						os.FileMode(0644),
					},
				},
				{
					Name: "MkdirAll",
					Arguments: []interface{}{
						filepath.Join(testStateDir, sandboxesDir, testID, "shm"),
						os.FileMode(0700),
					},
				},
				{
					Name: "Mount",
					// Ignore arguments which are too complex to check.
				},
			},
		},
		{
			desc:     "should create /etc/hostname when hostname is set",
			hostname: "test-hostname",
			ipcMode:  runtime.NamespaceMode_NODE,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						filepath.Join(testRootDir, sandboxesDir, testID, "hostname"),
						[]byte("test-hostname\n"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts",
						filepath.Join(testRootDir, sandboxesDir, testID, "hosts"),
						os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/resolv.conf",
						filepath.Join(testRootDir, sandboxesDir, testID, "resolv.conf"),
						os.FileMode(0644),
					},
				},
				{
					Name:      "Stat",
					Arguments: []interface{}{"/dev/shm"},
				},
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c := newControllerService()
			c.os.(*ostesting.FakeOS).HostnameFn = func() (string, error) {
				return realhostname, nil
			}
			cfg := &runtime.PodSandboxConfig{
				Hostname:  test.hostname,
				DnsConfig: test.dnsConfig,
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Ipc: test.ipcMode,
						},
					},
				},
			}
			c.setupSandboxFiles(testID, cfg)
			calls := c.os.(*ostesting.FakeOS).GetCalls()
			assert.Len(t, calls, len(test.expectedCalls))
			for i, expected := range test.expectedCalls {
				if expected.Arguments == nil {
					// Ignore arguments.
					expected.Arguments = calls[i].Arguments
				}
				assert.Equal(t, expected, calls[i])
			}
		})
	}
}

func TestParseDNSOption(t *testing.T) {
	for _, test := range []struct {
		desc            string
		servers         []string
		searches        []string
		options         []string
		expectedContent string
		expectErr       bool
	}{
		{
			desc: "empty dns options should return empty content",
		},
		{
			desc:     "non-empty dns options should return correct content",
			servers:  []string{"8.8.8.8", "server.google.com"},
			searches: []string{"114.114.114.114"},
			options:  []string{"timeout:1"},
			expectedContent: `search 114.114.114.114
nameserver 8.8.8.8
nameserver server.google.com
options timeout:1
`,
		},
		{
			desc:    "expanded dns config should return correct content on modern libc (e.g. glibc 2.26 and above)",
			servers: []string{"8.8.8.8", "server.google.com"},
			searches: []string{
				"server0.google.com",
				"server1.google.com",
				"server2.google.com",
				"server3.google.com",
				"server4.google.com",
				"server5.google.com",
				"server6.google.com",
			},
			options: []string{"timeout:1"},
			expectedContent: `search server0.google.com server1.google.com server2.google.com server3.google.com server4.google.com server5.google.com server6.google.com
nameserver 8.8.8.8
nameserver server.google.com
options timeout:1
`,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			resolvContent, err := parseDNSOptions(test.servers, test.searches, test.options)
			if test.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, resolvContent, test.expectedContent)
		})
	}
}

// TODO(random-liu): [P1] Add unit test for different error cases to make sure
// the function cleans up on error properly.
