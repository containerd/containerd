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
	"os"
	"path/filepath"
	"testing"

	cni "github.com/containerd/go-cni"
	"github.com/containerd/typeurl"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	"github.com/containerd/cri/pkg/annotations"
	criconfig "github.com/containerd/cri/pkg/config"
	ostesting "github.com/containerd/cri/pkg/os/testing"
	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
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
		assert.Equal(t, getCgroupsPath("/test/cgroup/parent", id, false), spec.Linux.CgroupsPath)
		assert.Equal(t, relativeRootfsPath, spec.Root.Path)
		assert.Equal(t, true, spec.Root.Readonly)
		assert.Contains(t, spec.Process.Env, "a=b", "c=d")
		assert.Equal(t, []string{"/pause", "forever"}, spec.Process.Args)
		assert.Equal(t, "/workspace", spec.Process.Cwd)
		assert.EqualValues(t, *spec.Linux.Resources.CPU.Shares, defaultSandboxCPUshares)
		assert.EqualValues(t, *spec.Process.OOMScoreAdj, defaultSandboxOOMAdj)

		t.Logf("Check PodSandbox annotations")
		assert.Contains(t, spec.Annotations, annotations.SandboxID)
		assert.EqualValues(t, spec.Annotations[annotations.SandboxID], id)

		assert.Contains(t, spec.Annotations, annotations.ContainerType)
		assert.EqualValues(t, spec.Annotations[annotations.ContainerType], annotations.ContainerTypeSandbox)
	}
	return config, imageConfig, specCheck
}

func TestGenerateSandboxContainerSpec(t *testing.T) {
	testID := "test-id"
	nsPath := "test-cni"
	for desc, test := range map[string]struct {
		configChange      func(*runtime.PodSandboxConfig)
		imageConfigChange func(*imagespec.ImageConfig)
		specCheck         func(*testing.T, *runtimespec.Spec)
		expectErr         bool
	}{
		"spec should reflect original config": {
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				// runtime spec should have expected namespaces enabled by default.
				require.NotNil(t, spec.Linux)
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.NetworkNamespace,
					Path: nsPath,
				})
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.PIDNamespace,
				})
				assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.IPCNamespace,
				})
			},
		},
		"host namespace": {
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
					Type: runtimespec.PIDNamespace,
				})
				assert.NotContains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{
					Type: runtimespec.IPCNamespace,
				})
			},
		},
		"should return error when entrypoint is empty": {
			imageConfigChange: func(c *imagespec.ImageConfig) {
				c.Entrypoint = nil
			},
			expectErr: true,
		},
		"should return error when env is invalid ": {
			// Also covers addImageEnvs.
			imageConfigChange: func(c *imagespec.ImageConfig) {
				c.Env = []string{"a"}
			},
			expectErr: true,
		},
		"should set supplemental groups correctly": {
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
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIService()
		config, imageConfig, specCheck := getRunPodSandboxTestData()
		if test.configChange != nil {
			test.configChange(config)
		}
		if test.imageConfigChange != nil {
			test.imageConfigChange(imageConfig)
		}
		spec, err := c.generateSandboxContainerSpec(testID, config, imageConfig, nsPath)
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, spec)
			continue
		}
		assert.NoError(t, err)
		assert.NotNil(t, spec)
		specCheck(t, testID, spec)
		if test.specCheck != nil {
			test.specCheck(t, spec)
		}
	}
}

func TestSetupSandboxFiles(t *testing.T) {
	const testID = "test-id"
	for desc, test := range map[string]struct {
		dnsConfig     *runtime.DNSConfig
		ipcMode       runtime.NamespaceMode
		expectedCalls []ostesting.CalledDetail
	}{
		"should check host /dev/shm existence when ipc mode is NODE": {
			ipcMode: runtime.NamespaceMode_NODE,
			expectedCalls: []ostesting.CalledDetail{
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
		"should create new /etc/resolv.conf if DNSOptions is set": {
			dnsConfig: &runtime.DNSConfig{
				Servers:  []string{"8.8.8.8"},
				Searches: []string{"114.114.114.114"},
				Options:  []string{"timeout:1"},
			},
			ipcMode: runtime.NamespaceMode_NODE,
			expectedCalls: []ostesting.CalledDetail{
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
		"should create sandbox shm when ipc namespace mode is not NODE": {
			ipcMode: runtime.NamespaceMode_POD,
			expectedCalls: []ostesting.CalledDetail{
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
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIService()
		cfg := &runtime.PodSandboxConfig{
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
	}
}

func TestParseDNSOption(t *testing.T) {
	for desc, test := range map[string]struct {
		servers         []string
		searches        []string
		options         []string
		expectedContent string
		expectErr       bool
	}{
		"empty dns options should return empty content": {},
		"non-empty dns options should return correct content": {
			servers:  []string{"8.8.8.8", "server.google.com"},
			searches: []string{"114.114.114.114"},
			options:  []string{"timeout:1"},
			expectedContent: `search 114.114.114.114
nameserver 8.8.8.8
nameserver server.google.com
options timeout:1
`,
		},
		"should return error if dns search exceeds limit(6)": {
			searches: []string{
				"server0.google.com",
				"server1.google.com",
				"server2.google.com",
				"server3.google.com",
				"server4.google.com",
				"server5.google.com",
				"server6.google.com",
			},
			expectErr: true,
		},
	} {
		t.Logf("TestCase %q", desc)
		resolvContent, err := parseDNSOptions(test.servers, test.searches, test.options)
		if test.expectErr {
			assert.Error(t, err)
			continue
		}
		assert.NoError(t, err)
		assert.Equal(t, resolvContent, test.expectedContent)
	}
}

func TestToCNIPortMappings(t *testing.T) {
	for desc, test := range map[string]struct {
		criPortMappings []*runtime.PortMapping
		cniPortMappings []cni.PortMapping
	}{
		"empty CRI port mapping should map to empty CNI port mapping": {},
		"CRI port mapping should be converted to CNI port mapping properly": {
			criPortMappings: []*runtime.PortMapping{
				{
					Protocol:      runtime.Protocol_UDP,
					ContainerPort: 1234,
					HostPort:      5678,
					HostIp:        "123.124.125.126",
				},
				{
					Protocol:      runtime.Protocol_TCP,
					ContainerPort: 4321,
					HostPort:      8765,
					HostIp:        "126.125.124.123",
				},
			},
			cniPortMappings: []cni.PortMapping{
				{
					HostPort:      5678,
					ContainerPort: 1234,
					Protocol:      "udp",
					HostIP:        "123.124.125.126",
				},
				{
					HostPort:      8765,
					ContainerPort: 4321,
					Protocol:      "tcp",
					HostIP:        "126.125.124.123",
				},
			},
		},
		"CRI port mapping without host port should be skipped": {
			criPortMappings: []*runtime.PortMapping{
				{
					Protocol:      runtime.Protocol_UDP,
					ContainerPort: 1234,
					HostIp:        "123.124.125.126",
				},
				{
					Protocol:      runtime.Protocol_TCP,
					ContainerPort: 4321,
					HostPort:      8765,
					HostIp:        "126.125.124.123",
				},
			},
			cniPortMappings: []cni.PortMapping{
				{
					HostPort:      8765,
					ContainerPort: 4321,
					Protocol:      "tcp",
					HostIP:        "126.125.124.123",
				},
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		assert.Equal(t, test.cniPortMappings, toCNIPortMappings(test.criPortMappings))
	}
}

func TestTypeurlMarshalUnmarshalSandboxMeta(t *testing.T) {
	for desc, test := range map[string]struct {
		configChange func(*runtime.PodSandboxConfig)
	}{
		"should marshal original config": {},
		"should marshal Linux": {
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						Network: runtime.NamespaceMode_NODE,
						Pid:     runtime.NamespaceMode_NODE,
						Ipc:     runtime.NamespaceMode_NODE,
					},
					SupplementalGroups: []int64{1111, 2222},
				}
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		meta := &sandboxstore.Metadata{
			ID:        "1",
			Name:      "sandbox_1",
			NetNSPath: "/home/cloud",
		}
		meta.Config, _, _ = getRunPodSandboxTestData()
		if test.configChange != nil {
			test.configChange(meta.Config)
		}

		any, err := typeurl.MarshalAny(meta)
		assert.NoError(t, err)
		data, err := typeurl.UnmarshalAny(any)
		assert.NoError(t, err)
		assert.IsType(t, &sandboxstore.Metadata{}, data)
		curMeta, ok := data.(*sandboxstore.Metadata)
		assert.True(t, ok)
		assert.Equal(t, meta, curMeta)
	}
}

func TestHostPrivilegedSandbox(t *testing.T) {
	privilegedContext := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				Privileged: true,
			},
		},
	}
	nonPrivilegedContext := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				Privileged: false,
			},
		},
	}
	hostNamespace := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				Privileged: false,
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_NODE,
					Pid:     runtime.NamespaceMode_NODE,
					Ipc:     runtime.NamespaceMode_NODE,
				},
			},
		},
	}
	tests := []struct {
		name   string
		config *runtime.PodSandboxConfig
		want   bool
	}{
		{"Security Context is nil", nil, false},
		{"Security Context is privileged", privilegedContext, true},
		{"Security Context is not privileged", nonPrivilegedContext, false},
		{"Security Context namespace host access", hostNamespace, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostPrivilegedSandbox(tt.config); got != tt.want {
				t.Errorf("hostPrivilegedSandbox() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSandboxRuntime(t *testing.T) {
	untrustedWorkloadRuntime := criconfig.Runtime{
		Type:   "io.containerd.runtime.v1.linux",
		Engine: "untursted-workload-runtime",
		Root:   "",
	}

	defaultRuntime := criconfig.Runtime{
		Type:   "io.containerd.runtime.v1.linux",
		Engine: "default-runtime",
		Root:   "",
	}

	for desc, test := range map[string]struct {
		sandboxConfig            *runtime.PodSandboxConfig
		defaultRuntime           criconfig.Runtime
		untrustedWorkloadRuntime criconfig.Runtime
		expectErr                bool
		expectedRuntime          criconfig.Runtime
	}{
		"should return error if untrusted workload requires host privilege": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						Privileged: true,
					},
				},
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			defaultRuntime:           defaultRuntime,
			untrustedWorkloadRuntime: untrustedWorkloadRuntime,
			expectErr:                true,
		},
		"should use untrusted workload runtime for untrusted workload": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			defaultRuntime:           defaultRuntime,
			untrustedWorkloadRuntime: untrustedWorkloadRuntime,
			expectedRuntime:          untrustedWorkloadRuntime,
		},
		"should use default runtime for regular workload": {
			sandboxConfig:            &runtime.PodSandboxConfig{},
			defaultRuntime:           defaultRuntime,
			untrustedWorkloadRuntime: untrustedWorkloadRuntime,
			expectedRuntime:          defaultRuntime,
		},
		"should use default runtime for trusted workload": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "false",
				},
			},
			defaultRuntime:           defaultRuntime,
			untrustedWorkloadRuntime: untrustedWorkloadRuntime,
			expectedRuntime:          defaultRuntime,
		},
		"should return error if untrusted workload runtime is required but not configured": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			defaultRuntime: defaultRuntime,
			expectErr:      true,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			cri := newTestCRIService()
			cri.config = criconfig.Config{
				PluginConfig: criconfig.DefaultConfig(),
			}
			cri.config.ContainerdConfig.DefaultRuntime = test.defaultRuntime
			cri.config.ContainerdConfig.UntrustedWorkloadRuntime = test.untrustedWorkloadRuntime
			r, err := cri.getSandboxRuntime(test.sandboxConfig)
			assert.Equal(t, test.expectErr, err != nil)
			assert.Equal(t, test.expectedRuntime, r)
		})
	}
}

// TODO(random-liu): [P1] Add unit test for different error cases to make sure
// the function cleans up on error properly.
