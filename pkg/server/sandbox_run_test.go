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
	"testing"

	"github.com/containerd/typeurl"
	"github.com/cri-o/ocicni/pkg/ocicni"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
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
						HostNetwork: true,
						HostPid:     true,
						HostIpc:     true,
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
		c := newTestCRIContainerdService()
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
	testRootDir := "test-sandbox-root"
	for desc, test := range map[string]struct {
		dnsConfig     *runtime.DNSConfig
		hostIpc       bool
		expectedCalls []ostesting.CalledDetail
	}{
		"should check host /dev/shm existence when hostIpc is true": {
			hostIpc: true,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts", testRootDir + "/hosts", os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/resolv.conf", testRootDir + "/resolv.conf", os.FileMode(0644),
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
			hostIpc: true,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts", testRootDir + "/hosts", os.FileMode(0644),
					},
				},
				{
					Name: "WriteFile",
					Arguments: []interface{}{
						testRootDir + "/resolv.conf", []byte(`search 114.114.114.114
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
		"should create sandbox shm when hostIpc is false": {
			hostIpc: false,
			expectedCalls: []ostesting.CalledDetail{
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/hosts", testRootDir + "/hosts", os.FileMode(0644),
					},
				},
				{
					Name: "CopyFile",
					Arguments: []interface{}{
						"/etc/resolv.conf", testRootDir + "/resolv.conf", os.FileMode(0644),
					},
				},
				{
					Name: "MkdirAll",
					Arguments: []interface{}{
						testRootDir + "/shm", os.FileMode(0700),
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
		c := newTestCRIContainerdService()
		cfg := &runtime.PodSandboxConfig{
			DnsConfig: test.dnsConfig,
			Linux: &runtime.LinuxPodSandboxConfig{
				SecurityContext: &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						HostIpc: test.hostIpc,
					},
				},
			},
		}
		c.setupSandboxFiles(testRootDir, cfg)
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
		cniPortMappings []ocicni.PortMapping
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
			cniPortMappings: []ocicni.PortMapping{
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
			cniPortMappings: []ocicni.PortMapping{
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
						HostNetwork: true,
						HostPid:     true,
						HostIpc:     true,
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

// TODO(random-liu): [P1] Add unit test for different error cases to make sure
// the function cleans up on error properly.
