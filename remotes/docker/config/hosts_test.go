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

package config

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/remotes/docker"
)

const allCaps = docker.HostCapabilityPull | docker.HostCapabilityResolve | docker.HostCapabilityPush

func TestDefaultHosts(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)
	resolve := ConfigureHosts(ctx, HostOptions{})

	for _, tc := range []struct {
		host     string
		expected []docker.RegistryHost
	}{
		{
			host: "docker.io",
			expected: []docker.RegistryHost{
				{
					Scheme:       "https",
					Host:         "registry-1.docker.io",
					Path:         "/v2",
					Capabilities: allCaps,
				},
			},
		},
	} {
		hosts, err := resolve(tc.host)
		if err != nil {
			t.Errorf("[%s] resolve failed: %v", tc.host, err)
			continue
		}
		if len(hosts) != len(tc.expected) {
			t.Errorf("[%s] unexpected number of hosts %d, expected %d", tc.host, len(hosts), len(tc.expected))
			continue
		}
		for j := range hosts {
			if !compareRegistryHost(hosts[j], tc.expected[j]) {

				t.Errorf("[%s] [%d] unexpected host %v, expected %v", tc.host, j, hosts[j], tc.expected[j])
				break
			}

		}

	}
}

func TestParseHostFile(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)

	const testtoml = `
server = "https://test-default.registry"
ca = "/etc/path/default"
[header]
  x-custom-1 = "custom header"

[host."https://mirror.registry"]
  capabilities = ["pull"]
  ca = "/etc/certs/mirror.pem"
  skip_verify = false
  [host."https://mirror.registry".header]
    x-custom-2 = ["value1", "value2"]

[host."https://mirror-bak.registry/us"]
  capabilities = ["pull"]
  skip_verify = true

[host."http://mirror.registry"]
  capabilities = ["pull"]

[host."https://test-1.registry"]
  capabilities = ["pull", "resolve", "push"]
  ca = ["/etc/certs/test-1-ca.pem", "/etc/certs/special.pem"]
  client = [["/etc/certs/client.cert", "/etc/certs/client.key"],["/etc/certs/client.pem", ""]]

[host."https://test-2.registry"]
  client = "/etc/certs/client.pem"

[host."https://test-3.registry"]
  client = ["/etc/certs/client-1.pem", "/etc/certs/client-2.pem"]
`
	var tb, fb = true, false
	expected := []hostConfig{
		{
			scheme:       "https",
			host:         "mirror.registry",
			path:         "/v2",
			capabilities: docker.HostCapabilityPull,
			caCerts:      []string{filepath.FromSlash("/etc/certs/mirror.pem")},
			skipVerify:   &fb,
			header:       http.Header{"x-custom-2": {"value1", "value2"}},
		},
		{
			scheme:       "https",
			host:         "mirror-bak.registry",
			path:         "/us/v2",
			capabilities: docker.HostCapabilityPull,
			skipVerify:   &tb,
		},
		{
			scheme:       "http",
			host:         "mirror.registry",
			path:         "/v2",
			capabilities: docker.HostCapabilityPull,
		},
		{
			scheme:       "https",
			host:         "test-1.registry",
			path:         "/v2",
			capabilities: allCaps,
			caCerts:      []string{filepath.FromSlash("/etc/certs/test-1-ca.pem"), filepath.FromSlash("/etc/certs/special.pem")},
			clientPairs: [][2]string{
				{filepath.FromSlash("/etc/certs/client.cert"), filepath.FromSlash("/etc/certs/client.key")},
				{filepath.FromSlash("/etc/certs/client.pem"), ""},
			},
		},
		{
			scheme:       "https",
			host:         "test-2.registry",
			path:         "/v2",
			capabilities: allCaps,
			clientPairs: [][2]string{
				{filepath.FromSlash("/etc/certs/client.pem")},
			},
		},
		{
			scheme:       "https",
			host:         "test-3.registry",
			path:         "/v2",
			capabilities: allCaps,
			clientPairs: [][2]string{
				{filepath.FromSlash("/etc/certs/client-1.pem")},
				{filepath.FromSlash("/etc/certs/client-2.pem")},
			},
		},
		{
			scheme:       "https",
			host:         "test-default.registry",
			path:         "/v2",
			capabilities: allCaps,
			caCerts:      []string{filepath.FromSlash("/etc/path/default")},
			header:       http.Header{"x-custom-1": {"custom header"}},
		},
	}
	hosts, err := parseHostsFile(ctx, "", []byte(testtoml))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if t.Failed() {
			t.Log("HostConfigs...\nActual:\n" + printHostConfig(hosts) + "Expected:\n" + printHostConfig(expected))
		}
	}()

	if len(hosts) != len(expected) {
		t.Fatalf("Unexpected number of hosts %d, expected %d", len(hosts), len(expected))
	}

	for i := range hosts {
		if !compareHostConfig(hosts[i], expected[i]) {
			t.Fatalf("Mismatch at host %d", i)
		}
	}
}

func compareRegistryHost(j, k docker.RegistryHost) bool {
	if j.Scheme != k.Scheme {
		return false
	}
	if j.Host != k.Host {
		return false
	}
	if j.Path != k.Path {
		return false
	}
	if j.Capabilities != k.Capabilities {
		return false
	}
	// Not comparing TLS configs or authorizations
	return true
}

func compareHostConfig(j, k hostConfig) bool {
	if j.scheme != k.scheme {
		return false
	}
	if j.host != k.host {
		return false
	}
	if j.path != k.path {
		return false
	}
	if j.capabilities != k.capabilities {
		return false
	}

	if len(j.caCerts) != len(k.caCerts) {
		return false
	}
	for i := range j.caCerts {
		if j.caCerts[i] != k.caCerts[i] {
			return false
		}
	}
	if len(j.clientPairs) != len(k.clientPairs) {
		return false
	}
	for i := range j.clientPairs {
		if j.clientPairs[i][0] != k.clientPairs[i][0] {
			return false
		}
		if j.clientPairs[i][1] != k.clientPairs[i][1] {
			return false
		}
	}
	if j.skipVerify != nil && k.skipVerify != nil {
		if *j.skipVerify != *k.skipVerify {
			return false
		}
	} else if j.skipVerify != nil || k.skipVerify != nil {
		return false
	}

	if len(j.header) != len(k.header) {
		return false
	}
	for key := range j.header {
		if len(j.header[key]) != len(k.header[key]) {
			return false
		}
		for i := range j.header[key] {
			if j.header[key][i] != k.header[key][i] {
				return false
			}
		}
	}

	return true
}

func printHostConfig(hc []hostConfig) string {
	b := bytes.NewBuffer(nil)
	for i := range hc {
		fmt.Fprintf(b, "\t[%d]\tscheme: %q\n", i, hc[i].scheme)
		fmt.Fprintf(b, "\t\thost: %q\n", hc[i].host)
		fmt.Fprintf(b, "\t\tpath: %q\n", hc[i].path)
		fmt.Fprintf(b, "\t\tcaps: %03b\n", hc[i].capabilities)
		fmt.Fprintf(b, "\t\tca: %#v\n", hc[i].caCerts)
		fmt.Fprintf(b, "\t\tclients: %#v\n", hc[i].clientPairs)
		if hc[i].skipVerify == nil {
			fmt.Fprintf(b, "\t\tskip-verify: %v\n", hc[i].skipVerify)
		} else {
			fmt.Fprintf(b, "\t\tskip-verify: %t\n", *hc[i].skipVerify)
		}
		fmt.Fprintf(b, "\t\theader: %#v\n", hc[i].header)
	}
	return b.String()
}
