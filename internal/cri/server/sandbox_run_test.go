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
	"testing"

	"github.com/containerd/go-cni"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

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
			ips:                   []string{"192.168.17.43", "2001:db8:85a3::8a2e:370:7334"},
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
