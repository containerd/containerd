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
	"net"
	"testing"

	cni "github.com/containerd/go-cni"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	"github.com/containerd/cri/pkg/annotations"
	criconfig "github.com/containerd/cri/pkg/config"
)

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
		"CRI port mapping with unsupported protocol should be skipped": {
			criPortMappings: []*runtime.PortMapping{
				{
					Protocol:      runtime.Protocol_SCTP,
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

func TestSelectPodIP(t *testing.T) {
	for desc, test := range map[string]struct {
		ips      []string
		expected string
	}{
		"ipv4 should be picked even if ipv6 comes first": {
			ips:      []string{"2001:db8:85a3::8a2e:370:7334", "192.168.17.43"},
			expected: "192.168.17.43",
		},
		"ipv6 should be picked when there is no ipv4": {
			ips:      []string{"2001:db8:85a3::8a2e:370:7334"},
			expected: "2001:db8:85a3::8a2e:370:7334",
		},
	} {
		t.Logf("TestCase %q", desc)
		var ipConfigs []*cni.IPConfig
		for _, ip := range test.ips {
			ipConfigs = append(ipConfigs, &cni.IPConfig{
				IP: net.ParseIP(ip),
			})
		}
		assert.Equal(t, test.expected, selectPodIP(ipConfigs))
	}
}

func TestHostAccessingSandbox(t *testing.T) {
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
		{"Security Context is privileged", privilegedContext, false},
		{"Security Context is not privileged", nonPrivilegedContext, false},
		{"Security Context namespace host access", hostNamespace, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostAccessingSandbox(tt.config); got != tt.want {
				t.Errorf("hostAccessingSandbox() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSandboxRuntime(t *testing.T) {
	untrustedWorkloadRuntime := criconfig.Runtime{
		Type:   "io.containerd.runtime.v1.linux",
		Engine: "untrusted-workload-runtime",
		Root:   "",
	}

	defaultRuntime := criconfig.Runtime{
		Type:   "io.containerd.runtime.v1.linux",
		Engine: "default-runtime",
		Root:   "",
	}

	fooRuntime := criconfig.Runtime{
		Type:   "io.containerd.runtime.v1.linux",
		Engine: "foo-bar",
		Root:   "",
	}

	for desc, test := range map[string]struct {
		sandboxConfig   *runtime.PodSandboxConfig
		runtimeHandler  string
		runtimes        map[string]criconfig.Runtime
		expectErr       bool
		expectedRuntime criconfig.Runtime
	}{
		"should return error if untrusted workload requires host access": {
			sandboxConfig: &runtime.PodSandboxConfig{
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
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectErr: true,
		},
		"should use untrusted workload runtime for untrusted workload": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectedRuntime: untrustedWorkloadRuntime,
		},
		"should use default runtime for regular workload": {
			sandboxConfig: &runtime.PodSandboxConfig{},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault: defaultRuntime,
			},
			expectedRuntime: defaultRuntime,
		},
		"should use default runtime for trusted workload": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "false",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectedRuntime: defaultRuntime,
		},
		"should return error if untrusted workload runtime is required but not configured": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault: defaultRuntime,
			},
			expectErr: true,
		},
		"should use 'untrusted' runtime for untrusted workload": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectedRuntime: untrustedWorkloadRuntime,
		},
		"should use 'untrusted' runtime for untrusted workload & handler": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimeHandler: "untrusted",
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectedRuntime: untrustedWorkloadRuntime,
		},
		"should return an error if untrusted annotation with conflicting handler": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimeHandler: "foo",
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
				"foo":                      fooRuntime,
			},
			expectErr: true,
		},
		"should use correct runtime for a runtime handler": {
			sandboxConfig:  &runtime.PodSandboxConfig{},
			runtimeHandler: "foo",
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
				"foo":                      fooRuntime,
			},
			expectedRuntime: fooRuntime,
		},
		"should return error if runtime handler is required but not configured": {
			sandboxConfig:  &runtime.PodSandboxConfig{},
			runtimeHandler: "bar",
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault: defaultRuntime,
				"foo":                    fooRuntime,
			},
			expectErr: true,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			cri := newTestCRIService()
			cri.config = criconfig.Config{
				PluginConfig: criconfig.DefaultConfig(),
			}
			cri.config.ContainerdConfig.DefaultRuntimeName = criconfig.RuntimeDefault
			cri.config.ContainerdConfig.Runtimes = test.runtimes
			r, err := cri.getSandboxRuntime(test.sandboxConfig, test.runtimeHandler)
			assert.Equal(t, test.expectErr, err != nil)
			assert.Equal(t, test.expectedRuntime, r)
		})
	}
}
