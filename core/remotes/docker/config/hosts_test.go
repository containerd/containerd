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
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/containerd/log/logtest"

	"github.com/containerd/containerd/v2/core/remotes/docker"
)

const allCaps = docker.HostCapabilityPull |
	docker.HostCapabilityResolve |
	docker.HostCapabilityPush |
	docker.HostCapabilityReferrers

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
  capabilities = ["pull", "resolve", "push", "referrers"]
  ca = ["/etc/certs/test-1-ca.pem", "/etc/certs/special.pem"]
  client = [["/etc/certs/client.cert", "/etc/certs/client.key"],["/etc/certs/client.pem", ""]]

[host."https://test-2.registry"]
  client = "/etc/certs/client.pem"

[host."https://test-3.registry"]
  client = ["/etc/certs/client-1.pem", "/etc/certs/client-2.pem"]

[host."https://no-referrers.registry"]
  capabilities = ["pull", "resolve", "push"]
  client = ["/etc/certs/client-1.pem", "/etc/certs/client-2.pem"]

[host."https://noncompliantmirror.registry/v2/namespaceprefix"]
  capabilities = ["pull"]
  override_path = true

[host."https://noprefixnoncompliant.registry"]
  override_path = true

[host."https://onlyheader.registry".header]
  x-custom-1 = "justaheader"

[host."https://dial-timeout.registry"]
  dial_timeout = "3s"

[host."http://uds-pathname.registry"]
  dial_addr = "unix:///run/registry-cache.sock"

[host."http://uds-with-timeout.registry"]
  dial_addr = "unix:///run/r.sock"
  dial_timeout = "2s"
`

	var tb, fb = true, false
	var dialTimeout = 3 * time.Second
	var dialTimeout2s = 2 * time.Second
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
			host:         "no-referrers.registry",
			path:         "/v2",
			capabilities: docker.HostCapabilityPull | docker.HostCapabilityResolve | docker.HostCapabilityPush,
			clientPairs: [][2]string{
				{filepath.FromSlash("/etc/certs/client-1.pem")},
				{filepath.FromSlash("/etc/certs/client-2.pem")},
			},
		},
		{
			scheme:       "https",
			host:         "noncompliantmirror.registry",
			path:         "/v2/namespaceprefix",
			capabilities: docker.HostCapabilityPull,
		},
		{
			scheme:       "https",
			host:         "noprefixnoncompliant.registry",
			capabilities: allCaps,
		},
		{
			scheme:       "https",
			host:         "onlyheader.registry",
			path:         "/v2",
			capabilities: allCaps,
			header:       http.Header{"x-custom-1": {"justaheader"}},
		},
		{
			scheme:       "https",
			host:         "dial-timeout.registry",
			path:         "/v2",
			capabilities: allCaps,
			dialTimeout:  &dialTimeout,
		},
		{
			scheme:       "http",
			host:         "uds-pathname.registry",
			path:         "/v2",
			capabilities: allCaps,
			dialAddr:     "/run/registry-cache.sock",
		},
		{
			scheme:       "http",
			host:         "uds-with-timeout.registry",
			path:         "/v2",
			capabilities: allCaps,
			dialAddr:     "/run/r.sock",
			dialTimeout:  &dialTimeout2s,
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
	hosts, err := parseHostsFile("", []byte(testtoml))
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

func TestLoadCertFiles(t *testing.T) {
	dir := t.TempDir()

	type testCase struct {
		input hostConfig
	}
	cases := map[string]testCase{
		"crt only": {
			input: hostConfig{host: "testing.io", caCerts: []string{filepath.Join(dir, "testing.io", "ca.crt")}},
		},
		"crt and cert pair": {
			input: hostConfig{
				host:    "testing.io",
				caCerts: []string{filepath.Join(dir, "testing.io", "ca.crt")},
				clientPairs: [][2]string{
					{
						filepath.Join(dir, "testing.io", "client.cert"),
						filepath.Join(dir, "testing.io", "client.key"),
					},
				},
			},
		},
		"cert pair only": {
			input: hostConfig{
				host: "testing.io",
				clientPairs: [][2]string{
					{
						filepath.Join(dir, "testing.io", "client.cert"),
						filepath.Join(dir, "testing.io", "client.key"),
					},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {

			hostDir := filepath.Join(dir, tc.input.host)
			if err := os.MkdirAll(hostDir, 0700); err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(hostDir)

			for _, f := range tc.input.caCerts {
				if err := os.WriteFile(f, testKey, 0600); err != nil {
					t.Fatal(err)
				}
			}

			for _, pair := range tc.input.clientPairs {
				if err := os.WriteFile(pair[0], testKey, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pair[1], testKey, 0600); err != nil {
					t.Fatal(err)
				}
			}

			configs, err := loadHostDir(context.Background(), hostDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(configs) != 1 {
				t.Fatalf("\nexpected:\n%+v\ngot:\n%+v", tc.input, configs)
			}

			cfg := configs[0]
			cfg.host = tc.input.host

			if !compareHostConfig(cfg, tc.input) {
				t.Errorf("\nexpected:\n%+v:\n\ngot:\n%+v", tc.input, cfg)
			}
		})
	}
}

func TestHTTPFallback(t *testing.T) {
	for _, tc := range []struct {
		host           string
		opts           HostOptions
		expectedScheme string
		usesFallback   bool
	}{
		{
			host: "localhost:8080",
			opts: HostOptions{
				DefaultScheme: "http",
				DefaultTLS: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			expectedScheme: "https",
			usesFallback:   true,
		},
		{
			host: "localhost:8080",
			opts: HostOptions{
				DefaultScheme: "https",
				DefaultTLS: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			expectedScheme: "https",
			usesFallback:   false,
		},
		{
			host:           "localhost:8080",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   true,
		},
		{
			host:           "localhost:80",
			opts:           HostOptions{},
			expectedScheme: "http",
			usesFallback:   false,
		},
		{
			host:           "localhost:443",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   false,
		},
		{
			host: "localhost:80",
			opts: HostOptions{
				DefaultScheme: "http",
				DefaultTLS: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			expectedScheme: "http",
			usesFallback:   false,
		},
		{
			host: "localhost",
			opts: HostOptions{
				DefaultScheme: "http",
				DefaultTLS: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			expectedScheme: "https",
			usesFallback:   true,
		},
		{
			host: "localhost",
			opts: HostOptions{
				DefaultScheme: "https",
				DefaultTLS: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			expectedScheme: "https",
			usesFallback:   false,
		},
		{
			host:           "localhost",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   false,
		},
		{
			host:           "localhost:5000",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   true,
		},
		{
			host:           "example.com",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   false,
		},

		{
			host: "example.com",
			opts: HostOptions{
				DefaultScheme: "http",
				DefaultTLS: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			expectedScheme: "https",
			usesFallback:   true,
		},
		{
			host: "example.com:5000",
			opts: HostOptions{
				DefaultScheme: "http",
				DefaultTLS: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			expectedScheme: "https",
			usesFallback:   true,
		},
		{
			host:           "example.com:5000",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   false,
		},
		{
			host: "example2.com",
			opts: HostOptions{
				DefaultScheme: "http",
			},
			expectedScheme: "http",
			usesFallback:   false,
		},
		{
			host:           "127.0.0.254:5000",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   true,
		},
		{
			host:           "127.0.0.254",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   false,
		},
		{
			host:           "[::1]:5000",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   true,
		},
		{
			host:           "::1",
			opts:           HostOptions{},
			expectedScheme: "https",
			usesFallback:   false,
		},
	} {
		testName := tc.host
		if tc.opts.DefaultScheme != "" {
			testName = testName + "-default-" + tc.opts.DefaultScheme
		}
		t.Run(testName, func(t *testing.T) {
			ctx := logtest.WithT(context.TODO(), t)
			hosts := ConfigureHosts(ctx, tc.opts)
			testHosts, err := hosts(tc.host)
			if err != nil {
				t.Fatal(err)
			}
			if len(testHosts) != 1 {
				t.Fatalf("expected a single host for localhost config, got %d hosts", len(testHosts))
			}
			if testHosts[0].Scheme != tc.expectedScheme {
				t.Fatalf("expected %s scheme for localhost with tls config, got %q", tc.expectedScheme, testHosts[0].Scheme)
			}
			_, defaultTransport := testHosts[0].Client.Transport.(*http.Transport)
			if tc.usesFallback && defaultTransport {
				t.Fatal("expected http fallback configured for defaulted localhost endpoint")
			} else if !defaultTransport && !tc.usesFallback {
				t.Fatalf("expected no http fallback configured for defaulted localhost endpoint")
			}
		})
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

	if j.dialTimeout != nil && k.dialTimeout != nil {
		if *j.dialTimeout != *k.dialTimeout {
			return false
		}
	} else if j.dialTimeout != nil || k.dialTimeout != nil {
		return false
	}

	if j.dialAddr != k.dialAddr {
		return false
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
		if hc[i].dialTimeout != nil {
			fmt.Fprintf(b, "\t\tdial-timeout: %v\n", hc[i].dialTimeout)
		}
		if hc[i].dialAddr != "" {
			fmt.Fprintf(b, "\t\tdial-addr: %q\n", hc[i].dialAddr)
		}
	}
	return b.String()
}

var (
	testKey = []byte(`-----BEGIN PRIVATE KEY-----
	MIIEvwIBADANBgkqhkiG9w0BAQEFAASCBKkwggSlAgEAAoIBAQDa+zvPgFXwra4S
	0DzEWRgZHxVTDG1sJsnN/jOaHCNpRyABGVW5kdei9WFWv3dpiELI+guQMjdUL++w
	M68bs6cXKW+1nW6u5uWuGwklOwkoKoeHkkn/vHef7ybk+5qdk6AYY0DKQsrBBOvj
	f0WAnG+1xi8VIOEBmce0/47MexOiuILVkjokgdmDCOc8ShkT6/EJTCsI1wDew/4G
	9IiRzw2xSM0ZATAtEC3HEBRLJGWZQtuKlLCuzJ+erOWUcg2cjnSgR3PmaAXE//5g
	SoeqEbtTo1satf9AR4VvreIAI8m0eyo8ABMLTkZovEFcUUHetL63hdqItjCeRfrQ
	zK4LMRFbAgMBAAECggEBAJtP6UHo0gtcA8SQMSlJz4+xvhwjClDUyfjyPIMnRe5b
	ZdWhtG1jhT+tLhaqwfT1kfidcCobk6aAQU4FukK5jt8cooB7Yo9mcKylvDzNvFbi
	ozGCjj113JpwsnNiCG2O0NO7Qa6y5L810GCQWik3yvtvzuD7atsJyN0VDKD3Ahw7
	1X8z76grZFlhVMCTAA3vAJ2y2p3sd+TGC/PIhnsvChwxEorGCnMj93mBaUI7zZRY
	EZhlk4ZvC9sUvlVUuYC+wAHjasgN9s3AzsOBSx+Xt3NaXQHzhL0mVo/vu/pjjFBs
	WBLR1PBoIfveTJPOp+Hrr4cuCK0NuX9sWlWPYLl5A2ECgYEA5fq3n4PhbJ2BuTS5
	AVgOmjRpk1eogb6aSY+cx7Mr++ADF9EYXc5tgKoUsDeeiiyK2lv6IKavoTWT1kdd
	shiclyEzp2CxG5GtbC/g2XHiBLepgo1fjfev3btCmIeGVBjglOx4F3gEsRygrAID
	zcz94m2I+uqLT8hvWnccIqScglkCgYEA88H2ji4Nvx6TmqCLcER0vNDVoxxDfgGb
	iohvenD2jmmdTnezTddsgECAI8L0BPNS/0vBCduTjs5BqhKbIfQvuK5CANMUcxuQ
	twWH8kPvTYJVgsmWP6sSXSz3PohWC5EA9xACExGtyN6d7sLUCV0SBhjlcgMvGuDM
	lP6NjyyWctMCgYBKdfGr+QQsqZaNw48+6ybXMK8aIKCTWYYU2SW21sEf7PizZmTQ
	Qnzb0rWeFHQFYsSWTH9gwPdOZ8107GheuG9C02IpCDpvpawTwjC31pKKWnjMpz9P
	9OkBDpdSUVbhtahJL4L2fkpumck/x+s5X+y3uiVGsFfovgmnrbbzVH7ECQKBgQCC
	MYs7DaYR+obkA/P2FtozL2esIyB5YOpu58iDIWrPTeHTU2PVo8Y0Cj9m2m3zZvNh
	oFiOp1T85XV1HVL2o7IJdimSvyshAAwfdTjTUS2zvHVn0bwKbZj1Y1r7b15l9yEI
	1OgGv16O9zhrmmweRDOoRgvnBYRXWtJqkjuRyULiOQKBgQC/lSYigV32Eb8Eg1pv
	7OcPWv4qV4880lRE0MXuQ4VFa4+pqvdziYFYQD4jDYJ4IX9l//bsobL0j7z0P0Gk
	wDFti9bRwRoO1ntqoA8n2pDLlLRGl0dyjB6fHzp27oqtyf1HRlHiow7Gqx5b5JOk
	tycYKwA3DuaSyqPe6MthLneq8w==
	-----END PRIVATE KEY-----
	`)
)

func TestParseHostFileDialAddrInvalid(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{
			name:    "empty after scheme",
			value:   `unix://`,
			wantErr: "no socket path",
		},
		{
			name:    "missing scheme bare path",
			value:   `/run/registry.sock`,
			wantErr: "must start with",
		},
		{
			name:    "tcp scheme",
			value:   `tcp://127.0.0.1:5000`,
			wantErr: "must start with",
		},
		{
			name:    "http scheme",
			value:   `http://localhost`,
			wantErr: "must start with",
		},
		{
			name:    "scheme only no slashes",
			value:   `unix:`,
			wantErr: "must start with",
		},
		{
			name:    "malformed URL",
			value:   `unix://%ZZ`,
			wantErr: "unable to parse",
		},
		{
			name:    "relative path",
			value:   `unix://run/r.sock`,
			wantErr: "must be an absolute path",
		},
		{
			name:    "trailing slash pathname",
			value:   `unix:///run/r.sock/`,
			wantErr: "not a directory",
		},
		{
			name:    "query string",
			value:   `unix:///run/r.sock?foo=bar`,
			wantErr: "query",
		},
		{
			name:    "fragment",
			value:   `unix:///run/r.sock#frag`,
			wantErr: "fragment",
		},
		{
			name:    "empty abstract name",
			value:   `unix://@`,
			wantErr: "no abstract socket name",
		},
		{
			name:    "leading space",
			value:   ` unix:///run/r.sock`,
			wantErr: "leading or trailing spaces",
		},
		{
			name:    "trailing space",
			value:   `unix:///run/r.sock `,
			wantErr: "leading or trailing spaces",
		},
		{
			name:    "path too long",
			value:   `unix:///` + strings.Repeat("a", 120),
			wantErr: "too long",
		},
		{
			name:    "newline injection",
			value:   "unix:///run/r.sock\nfoo",
			wantErr: "unable to parse",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toml := fmt.Sprintf(`[host."http://example.registry"]
  dial_addr = %q
`, tc.value)
			_, err := parseHostsFile("", []byte(toml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestParseUnixDialAddrValid(t *testing.T) {
	cases := []struct {
		name      string
		value     string
		want      string
		linuxOnly bool
	}{
		{name: "pathname", value: `unix:///run/r.sock`, want: "/run/r.sock"},
		{name: "shortest absolute", value: `unix:///a`, want: "/a"},
		{name: "uppercase scheme", value: `UNIX:///run/r.sock`, want: "/run/r.sock"},
		// A path at the OS limit is still accepted; one byte over is rejected
		// by TestParseHostFileDialAddrInvalid/path_too_long.
		{name: "max length path", value: `unix://` + "/" + strings.Repeat("a", maxUnixSocketPathLen-1), want: "/" + strings.Repeat("a", maxUnixSocketPathLen-1)},
		{name: "abstract", value: `unix://@registry-cache`, want: "@registry-cache", linuxOnly: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.linuxOnly && runtime.GOOS != "linux" {
				t.Skipf("abstract sockets are only supported on Linux, runtime is %s", runtime.GOOS)
			}
			got, err := parseUnixDialAddr(tc.value)
			if err != nil {
				t.Fatalf("parseUnixDialAddr(%q): unexpected error %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("parseUnixDialAddr(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestParseUnixDialAddrInvalidDirect(t *testing.T) {
	// Inputs TOML cannot carry (e.g. a raw NUL byte) are tested by calling the
	// parser directly. These document that url.Parse rejects control characters.
	cases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "null byte in path", value: "unix:///run/r\x00.sock", wantErr: "unable to parse"},
		{name: "raw newline in path", value: "unix:///run/r.sock\nfoo", wantErr: "unable to parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseUnixDialAddr(tc.value); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("parseUnixDialAddr(%q): want error containing %q, got %v", tc.value, tc.wantErr, err)
			}
		})
	}
}

func TestParseUnixDialAddrAbstractNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("abstract socket guard only rejects on non-Linux platforms")
	}
	if _, err := parseUnixDialAddr("unix://@registry-cache"); err == nil {
		t.Fatalf("expected error for abstract socket on %s, got nil", runtime.GOOS)
	}
}

func TestConfigureHostsDialAddrWiresTransport(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)

	cases := []struct {
		name                string
		hostToml            string
		wantSharedClient    bool
		wantDialErrContains string
	}{
		{
			name: "only dial_addr set",
			hostToml: `
[host."http://uds.registry"]
  dial_addr = "unix:///nonexistent-uds-test.sock"
`,
			wantDialErrContains: "nonexistent-uds-test.sock",
		},
		{
			name: "dial_addr with dial_timeout",
			hostToml: `
[host."http://uds.registry"]
  dial_addr = "unix:///nonexistent-uds-timeout.sock"
  dial_timeout = "100ms"
`,
			wantDialErrContains: "nonexistent-uds-timeout.sock",
		},
		{
			name: "neither set (control)",
			hostToml: `
[host."http://plain.registry"]
`,
			wantSharedClient: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rhosts := configureFromHostToml(t, ctx, tc.hostToml)
			if tc.wantSharedClient {
				assertClientSharedBetweenHosts(t, rhosts)
				return
			}
			assertDialerTargetsUnixSocket(t, ctx, rhosts[0].Client, tc.wantDialErrContains)
		})
	}
}

func configureFromHostToml(t *testing.T, ctx context.Context, hostToml string) []docker.RegistryHost {
	t.Helper()
	hostDir := filepath.Join(t.TempDir(), "example.registry")
	if err := os.MkdirAll(hostDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hostToml), 0600); err != nil {
		t.Fatal(err)
	}
	opts := HostOptions{HostDir: func(string) (string, error) { return hostDir, nil }}
	rhosts, err := ConfigureHosts(ctx, opts)("example.registry")
	if err != nil {
		t.Fatalf("ConfigureHosts: %v", err)
	}
	if len(rhosts) == 0 {
		t.Fatal("expected at least one host")
	}
	return rhosts
}

func assertClientSharedBetweenHosts(t *testing.T, rhosts []docker.RegistryHost) {
	t.Helper()
	if len(rhosts) < 2 {
		t.Fatalf("expected at least 2 hosts to compare clients, got %d", len(rhosts))
	}
	if rhosts[0].Client != rhosts[1].Client {
		t.Errorf("expected shared *http.Client across hosts without dial_addr, got distinct clients")
	}
}

func assertDialerTargetsUnixSocket(t *testing.T, ctx context.Context, client *http.Client, wantErrSubstr string) {
	t.Helper()
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.DialContext == nil {
		t.Fatal("expected per-host DialContext to be set")
	}
	// A dial_addr host always connects directly to the socket, so the
	// transport must not route via HTTP(S)_PROXY (Proxy must be cleared).
	if tr.Proxy != nil {
		t.Error("expected Proxy to be nil for a dial_addr host")
	}
	_, derr := tr.DialContext(ctx, "tcp", "ignored:443")
	if derr == nil {
		t.Fatal("expected dial to a nonexistent unix socket to fail")
	}
	if !strings.Contains(derr.Error(), wantErrSubstr) {
		t.Errorf("expected dial error to mention %q, got %q", wantErrSubstr, derr.Error())
	}
}
