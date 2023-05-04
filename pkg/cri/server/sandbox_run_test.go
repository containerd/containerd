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
	"net"
	goruntime "runtime"
	"testing"

	cni "github.com/containerd/go-cni"
	"github.com/containerd/typeurl/v2"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
)

func TestSandboxContainerSpec(t *testing.T) {
	switch goruntime.GOOS {
	case "darwin":
		t.Skip("not implemented on Darwin")
	case "freebsd":
		t.Skip("not implemented on FreeBSD")
	}
	testID := "test-id"
	nsPath := "test-cni"
	for _, test := range []struct {
		desc              string
		configChange      func(*runtime.PodSandboxConfig)
		podAnnotations    []string
		imageConfigChange func(*imagespec.ImageConfig)
		specCheck         func(*testing.T, *runtimespec.Spec)
		expectErr         bool
	}{
		{
			desc: "should return error when entrypoint and cmd are empty",
			imageConfigChange: func(c *imagespec.ImageConfig) {
				c.Entrypoint = nil
				c.Cmd = nil
			},
			expectErr: true,
		},
		{
			desc:           "a passthrough annotation should be passed as an OCI annotation",
			podAnnotations: []string{"c"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, spec.Annotations["c"], "d")
			},
		},
		{
			desc: "a non-passthrough annotation should not be passed as an OCI annotation",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Annotations["d"] = "e"
			},
			podAnnotations: []string{"c"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, spec.Annotations["c"], "d")
				_, ok := spec.Annotations["d"]
				assert.False(t, ok)
			},
		},
		{
			desc: "passthrough annotations should support wildcard match",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Annotations["t.f"] = "j"
				c.Annotations["z.g"] = "o"
				c.Annotations["z"] = "o"
				c.Annotations["y.ca"] = "b"
				c.Annotations["y"] = "b"
			},
			podAnnotations: []string{"t*", "z.*", "y.c*"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, spec.Annotations["t.f"], "j")
				assert.Equal(t, spec.Annotations["z.g"], "o")
				assert.Equal(t, spec.Annotations["y.ca"], "b")
				_, ok := spec.Annotations["y"]
				assert.False(t, ok)
				_, ok = spec.Annotations["z"]
				assert.False(t, ok)
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			config, imageConfig, specCheck := getRunPodSandboxTestData()
			if test.configChange != nil {
				test.configChange(config)
			}

			if test.imageConfigChange != nil {
				test.imageConfigChange(imageConfig)
			}
			spec, err := c.sandboxContainerSpec(testID, config, imageConfig, nsPath,
				test.podAnnotations)
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

func TestTypeurlMarshalUnmarshalSandboxMeta(t *testing.T) {
	for _, test := range []struct {
		desc         string
		configChange func(*runtime.PodSandboxConfig)
	}{
		{
			desc: "should marshal original config",
		},
		{
			desc: "should marshal Linux",
			configChange: func(c *runtime.PodSandboxConfig) {
				if c.Linux == nil {
					c.Linux = &runtime.LinuxPodSandboxConfig{}
				}
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
		test := test
		t.Run(test.desc, func(t *testing.T) {
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
		})
	}
}

func TestToCNIPortMappings(t *testing.T) {
	for _, test := range []struct {
		desc            string
		criPortMappings []*runtime.PortMapping
		cniPortMappings []cni.PortMapping
	}{
		{
			desc: "empty CRI port mapping should map to empty CNI port mapping",
		},
		{
			desc: "CRI port mapping should be converted to CNI port mapping properly",
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
				{
					Protocol:      runtime.Protocol_SCTP,
					ContainerPort: 1234,
					HostPort:      5678,
					HostIp:        "123.124.125.126",
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
				{
					HostPort:      5678,
					ContainerPort: 1234,
					Protocol:      "sctp",
					HostIP:        "123.124.125.126",
				},
			},
		},
		{
			desc: "CRI port mapping without host port should be skipped",
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
		{
			desc: "CRI port mapping with unsupported protocol should be skipped",
			criPortMappings: []*runtime.PortMapping{
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
		test := test
		t.Run(test.desc, func(t *testing.T) {
			assert.Equal(t, test.cniPortMappings, toCNIPortMappings(test.criPortMappings))
		})
	}
}

func TestSelectPodIP(t *testing.T) {
	for _, test := range []struct {
		desc                  string
		ips                   []string
		expectedIP            string
		expectedAdditionalIPs []string
		pref                  string
	}{
		{
			desc:                  "ipv4 should be picked even if ipv6 comes first",
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334", "192.168.17.43"},
			expectedIP:            "192.168.17.43",
			expectedAdditionalIPs: []string{"2001:db8:85a3::8a2e:370:7334"},
		},
		{
			desc:                  "ipv6 should be picked even if ipv4 comes first",
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334", "192.168.17.43"},
			expectedIP:            "2001:db8:85a3::8a2e:370:7334",
			expectedAdditionalIPs: []string{"192.168.17.43"},
			pref:                  "ipv6",
		},
		{
			desc:                  "order should reflect ip selection",
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334", "192.168.17.43"},
			expectedIP:            "2001:db8:85a3::8a2e:370:7334",
			expectedAdditionalIPs: []string{"192.168.17.43"},
			pref:                  "cni",
		},

		{
			desc:                  "ipv4 should be picked when there is only ipv4",
			ips:                   []string{"192.168.17.43"},
			expectedIP:            "192.168.17.43",
			expectedAdditionalIPs: nil,
		},
		{
			desc:                  "ipv6 should be picked when there is no ipv4",
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334"},
			expectedIP:            "2001:db8:85a3::8a2e:370:7334",
			expectedAdditionalIPs: nil,
		},
		{
			desc:                  "the first ipv4 should be picked when there are multiple ipv4", // unlikely to happen
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334", "192.168.17.43", "2001:db8:85a3::8a2e:370:7335", "192.168.17.45"},
			expectedIP:            "192.168.17.43",
			expectedAdditionalIPs: []string{"2001:db8:85a3::8a2e:370:7334", "2001:db8:85a3::8a2e:370:7335", "192.168.17.45"},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			var ipConfigs []*cni.IPConfig
			for _, ip := range test.ips {
				ipConfigs = append(ipConfigs, &cni.IPConfig{
					IP: net.ParseIP(ip),
				})
			}
			ip, additionalIPs := selectPodIPs(context.Background(), ipConfigs, test.pref)
			assert.Equal(t, test.expectedIP, ip)
			assert.Equal(t, test.expectedAdditionalIPs, additionalIPs)
		})
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
