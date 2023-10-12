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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	servertesting "github.com/containerd/containerd/pkg/cri/testing"
)

func TestUpdateRuntimeConfig(t *testing.T) {
	const (
		testTemplate = `
{
	"name": "test-pod-network",
	"cniVersion": "1.0.0",
	"plugins": [
	{
		"type": "ptp",
		"mtu": 1460,
		"ipam": {
			"type": "host-local",
			"subnet": "{{.PodCIDR}}",
			"ranges": [{{range $i, $range := .PodCIDRRanges}}{{if $i}}, {{end}}[{"subnet": "{{$range}}"}]{{end}}],
			"routes": [{{range $i, $route := .Routes}}{{if $i}}, {{end}}{"dst": "{{$route}}"}{{end}}]
		}
	},
	]
}`
		testCIDR = "10.0.0.0/24, 2001:4860:4860::/64"
		expected = `
{
	"name": "test-pod-network",
	"cniVersion": "1.0.0",
	"plugins": [
	{
		"type": "ptp",
		"mtu": 1460,
		"ipam": {
			"type": "host-local",
			"subnet": "10.0.0.0/24",
			"ranges": [[{"subnet": "10.0.0.0/24"}], [{"subnet": "2001:4860:4860::/64"}]],
			"routes": [{"dst": "0.0.0.0/0"}, {"dst": "::/0"}]
		}
	},
	]
}`
	)

	for _, test := range []struct {
		name            string
		noTemplate      bool
		emptyCIDR       bool
		networkReady    bool
		expectCNIConfig bool
	}{
		{
			name:            "should not generate cni config if cidr is empty",
			emptyCIDR:       true,
			expectCNIConfig: false,
		},
		{
			name:            "should not generate cni config if template file is not specified",
			noTemplate:      true,
			expectCNIConfig: false,
		},
		{
			name:            "should not generate cni config if network is ready",
			networkReady:    true,
			expectCNIConfig: false,
		},
		{
			name:            "should generate cni config if template is specified and cidr is provided",
			expectCNIConfig: true,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			testDir := t.TempDir()
			templateName := filepath.Join(testDir, "template")
			err := os.WriteFile(templateName, []byte(testTemplate), 0666)
			require.NoError(t, err)
			confDir := filepath.Join(testDir, "net.d")
			confName := filepath.Join(confDir, cniConfigFileName)

			c := newTestCRIService()
			c.config.CniConfig = criconfig.CniConfig{
				NetworkPluginConfDir:      confDir,
				NetworkPluginConfTemplate: templateName,
			}
			req := &runtime.UpdateRuntimeConfigRequest{
				RuntimeConfig: &runtime.RuntimeConfig{
					NetworkConfig: &runtime.NetworkConfig{
						PodCidr: testCIDR,
					},
				},
			}
			if test.noTemplate {
				c.config.CniConfig.NetworkPluginConfTemplate = ""
			}
			if test.emptyCIDR {
				req.RuntimeConfig.NetworkConfig.PodCidr = ""
			}
			if !test.networkReady {
				c.netPlugin[defaultNetworkPlugin].(*servertesting.FakeCNIPlugin).StatusErr = errors.New("random error")
				c.netPlugin[defaultNetworkPlugin].(*servertesting.FakeCNIPlugin).LoadErr = errors.New("random error")
			}
			_, err = c.UpdateRuntimeConfig(context.Background(), req)
			assert.NoError(t, err)
			if !test.expectCNIConfig {
				_, err := os.Stat(confName)
				assert.Error(t, err)
			} else {
				got, err := os.ReadFile(confName)
				assert.NoError(t, err)
				assert.Equal(t, expected, string(got))
			}
		})
	}
}
