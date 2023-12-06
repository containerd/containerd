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
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/plugin"

	"github.com/containerd/containerd/pkg/deprecation"
)

func TestValidateConfig(t *testing.T) {
	for desc, test := range map[string]struct {
		config      *PluginConfig
		expectedErr string
		expected    *PluginConfig
		warnings    []deprecation.Warning
	}{
		"deprecated untrusted_workload_runtime": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					UntrustedWorkloadRuntime: Runtime{
						Type: "untrusted",
					},
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					UntrustedWorkloadRuntime: Runtime{
						Type: "untrusted",
					},
					Runtimes: map[string]Runtime{
						RuntimeUntrusted: {
							Type:        "untrusted",
							SandboxMode: string(ModePodSandbox),
						},
						RuntimeDefault: {
							Type:        "default",
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRIUntrustedWorkloadRuntime},
		},
		"both untrusted_workload_runtime and runtime[untrusted]": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					UntrustedWorkloadRuntime: Runtime{
						Type: "untrusted-1",
					},
					Runtimes: map[string]Runtime{
						RuntimeUntrusted: {
							Type: "untrusted-2",
						},
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
			},
			expectedErr: fmt.Sprintf("conflicting definitions: configuration includes both `untrusted_workload_runtime` and `runtimes[%q]`", RuntimeUntrusted),
		},
		"deprecated default_runtime": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntime: Runtime{
						Type: "default",
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntime: Runtime{
						Type: "default",
					},
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type:        "default",
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRIDefaultRuntime},
		},
		"no default_runtime_name": {
			config:      &PluginConfig{},
			expectedErr: "`default_runtime_name` is empty",
		},
		"no runtime[default_runtime_name]": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
				},
			},
			expectedErr: "no corresponding runtime configured in `containerd.runtimes` for `containerd` `default_runtime_name = \"default\"",
		},
		"deprecated systemd_cgroup for v1 runtime": {
			config: &PluginConfig{
				SystemdCgroup: true,
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: plugin.RuntimeLinuxV1,
						},
					},
				},
			},
			expected: &PluginConfig{
				SystemdCgroup: true,
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type:        plugin.RuntimeLinuxV1,
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRISystemdCgroupV1},
		},
		"deprecated systemd_cgroup for v2 runtime": {
			config: &PluginConfig{
				SystemdCgroup: true,
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: plugin.RuntimeRuncV1,
						},
					},
				},
			},
			expectedErr: fmt.Sprintf("`systemd_cgroup` only works for runtime %s", plugin.RuntimeLinuxV1),
		},
		"no_pivot for v1 runtime": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					NoPivot:            true,
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: plugin.RuntimeLinuxV1,
						},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					NoPivot:            true,
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type:        plugin.RuntimeLinuxV1,
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
			},
		},
		"no_pivot for v2 runtime": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					NoPivot:            true,
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: plugin.RuntimeRuncV1,
						},
					},
				},
			},
			expectedErr: fmt.Sprintf("`no_pivot` only works for runtime %s", plugin.RuntimeLinuxV1),
		},
		"deprecated runtime_engine for v1 runtime": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Engine: "runc",
							Type:   plugin.RuntimeLinuxV1,
						},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Engine:      "runc",
							Type:        plugin.RuntimeLinuxV1,
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRIRuntimeEngine},
		},
		"deprecated runtime_engine for v2 runtime": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Engine: "runc",
							Type:   plugin.RuntimeRuncV1,
						},
					},
				},
			},
			expectedErr: fmt.Sprintf("`runtime_engine` only works for runtime %s", plugin.RuntimeLinuxV1),
		},
		"deprecated runtime_root for v1 runtime": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Root: "/run/containerd/runc",
							Type: plugin.RuntimeLinuxV1,
						},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Root:        "/run/containerd/runc",
							Type:        plugin.RuntimeLinuxV1,
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRIRuntimeRoot},
		},
		"deprecated runtime_root for v2 runtime": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Root: "/run/containerd/runc",
							Type: plugin.RuntimeRuncV1,
						},
					},
				},
			},
			expectedErr: fmt.Sprintf("`runtime_root` only works for runtime %s", plugin.RuntimeLinuxV1),
		},
		"deprecated auths": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: plugin.RuntimeRuncV1,
						},
					},
				},
				Registry: Registry{
					Auths: map[string]AuthConfig{
						"https://gcr.io": {Username: "test"},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type:        plugin.RuntimeRuncV1,
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
				Registry: Registry{
					Configs: map[string]RegistryConfig{
						"gcr.io": {
							Auth: &AuthConfig{
								Username: "test",
							},
						},
					},
					Auths: map[string]AuthConfig{
						"https://gcr.io": {Username: "test"},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRIRegistryAuths},
		},
		"invalid stream_idle_timeout": {
			config: &PluginConfig{
				StreamIdleTimeout: "invalid",
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
			},
			expectedErr: "invalid stream idle timeout",
		},
		"conflicting mirror registry config": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
				Registry: Registry{
					ConfigPath: "/etc/containerd/conf.d",
					Mirrors: map[string]Mirror{
						"something.io": {},
					},
				},
			},
			expectedErr: "`mirrors` cannot be set when `config_path` is provided",
		},
		"conflicting tls registry config": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
				Registry: Registry{
					ConfigPath: "/etc/containerd/conf.d",
					Configs: map[string]RegistryConfig{
						"something.io": {
							TLS: &TLSConfig{},
						},
					},
				},
			},
			expectedErr: "`configs.tls` cannot be set when `config_path` is provided",
		},
		"deprecated mirrors": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {},
					},
				},
				Registry: Registry{
					Mirrors: map[string]Mirror{
						"example.com": {},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
				Registry: Registry{
					Mirrors: map[string]Mirror{
						"example.com": {},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRIRegistryMirrors},
		},
		"deprecated configs": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {},
					},
				},
				Registry: Registry{
					Configs: map[string]RegistryConfig{
						"gcr.io": {
							Auth: &AuthConfig{
								Username: "test",
							},
						},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							SandboxMode: string(ModePodSandbox),
						},
					},
				},
				Registry: Registry{
					Configs: map[string]RegistryConfig{
						"gcr.io": {
							Auth: &AuthConfig{
								Username: "test",
							},
						},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRIRegistryConfigs},
		},
		"privileged_without_host_devices_all_devices_allowed without privileged_without_host_devices": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							PrivilegedWithoutHostDevices:                  false,
							PrivilegedWithoutHostDevicesAllDevicesAllowed: true,
							Type: "default",
						},
					},
				},
			},
			expectedErr: "`privileged_without_host_devices_all_devices_allowed` requires `privileged_without_host_devices` to be enabled",
		},
		"invalid drain_exec_sync_io_timeout input": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
				DrainExecSyncIOTimeout: "10",
			},
			expectedErr: "invalid `drain_exec_sync_io_timeout`",
		},
		"deprecated CRIU path": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							SandboxMode: string(ModePodSandbox),
							Options: map[string]interface{}{
								"CriuPath": "/path/to/criu-binary",
							},
						},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							SandboxMode: string(ModePodSandbox),
							Options: map[string]interface{}{
								"CriuPath": "/path/to/criu-binary",
							},
						},
					},
				},
			},
			warnings: []deprecation.Warning{deprecation.CRICRIUPath},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			w, err := ValidatePluginConfig(context.Background(), test.config)
			if test.expectedErr != "" {
				assert.Contains(t, err.Error(), test.expectedErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, test.config)
			}
			if len(test.warnings) > 0 {
				assert.ElementsMatch(t, test.warnings, w)
			} else {
				assert.Len(t, w, 0)
			}
		})
	}
}
