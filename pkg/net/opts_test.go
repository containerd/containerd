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

package net

import (
	"testing"

	gocni "github.com/containerd/go-cni"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerOpts(t *testing.T) {
	opts := []ManagerOpt{
		WithPluginDirs("dir1", "dir2"),
	}
	c := ManagerConfig{}
	for _, o := range opts {
		require.NoError(t, o(&c))
	}
	assert.Equal(t, c.PluginDirs, []string{"dir1", "dir2"})
}

func TestNetworkOpts(t *testing.T) {
	opts := []NetworkOpt{
		WithConflist(testNetworkConfList(t)),
		WithNetworkLabels(map[string]string{"lkey": "lval"}),
	}
	c := NetworkConfig{}
	for _, o := range opts {
		require.NoError(t, o(&c))
	}
	assert.Equal(t, c.Labels, map[string]string{"lkey": "lval"})
	assert.Equal(t, c.Conflist, testNetworkConfList(t))
}

func TestAttachmentOpts(t *testing.T) {
	portMaps := []gocni.PortMapping{{HostPort: 80, ContainerPort: 8080, Protocol: "tcp", HostIP: "192.168.1.1"}}
	ipRanges := []gocni.IPRanges{{Subnet: "172.0.1.0/24", RangeStart: "172.0.1.10", RangeEnd: "172.0.1.128", Gateway: "172.0.1.1"}}
	bandWidth := gocni.BandWidth{IngressRate: 10, IngressBurst: 50, EgressRate: 20, EgressBurst: 80}
	dns := gocni.DNS{Servers: []string{"8.8.8.8"}, Searches: []string{"example.com"}, Options: []string{"option1"}}

	opts := []AttachmentOpt{
		WithContainer(TestContainer),
		WithNSPath(TestNSPath),
		WithIFName(TestInterface),
		WithCapabilityPortMap(portMaps),
		WithCapabilityIPRanges(ipRanges),
		WithCapabilityBandWidth(bandWidth),
		WithCapabilityDNS(dns),
		WithCapability("testcap", "testcapvalue"),
		WithLabels(map[string]string{"lkey": "lval"}),
		WithArgs("testarg", "testargval"),
	}

	c := AttachmentArgs{}
	for _, o := range opts {
		require.NoError(t, o(&c))
	}

	tgt := AttachmentArgs{
		ContainerID: TestContainer,
		NSPath:      TestNSPath,
		IFName:      TestInterface,
		CapabilityArgs: map[string]interface{}{
			"portMappings": portMaps,
			"ipRanges":     ipRanges,
			"bandwidth":    bandWidth,
			"dns":          dns,
			"testcap":      "testcapvalue",
		},
		PluginArgs: map[string]string{
			"lkey":    "lval",
			"testarg": "testargval",
		},
	}

	assert.Equal(t, c, tgt)
}
