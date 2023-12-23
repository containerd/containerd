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
	"testing"

	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/pkg/deprecation"
)

func TestValidateConfig(t *testing.T) {
	for desc, test := range map[string]struct {
		config           *PluginConfig
		expectedErr      string
		expected         *PluginConfig
		imageConfig      *ImageConfig
		imageExpectedErr string
		imageExpected    *ImageConfig
		warnings         []deprecation.Warning
	}{
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

		"deprecated auths": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {},
					},
				},
			},
			expected: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Sandboxer: string(ModePodSandbox),
						},
					},
				},
			},
			imageConfig: &ImageConfig{
				Registry: Registry{
					Auths: map[string]AuthConfig{
						"https://gcr.io": {Username: "test"},
					},
				},
			},
			imageExpected: &ImageConfig{
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
			imageConfig: &ImageConfig{
				Registry: Registry{
					ConfigPath: "/etc/containerd/conf.d",
					Mirrors: map[string]Mirror{
						"something.io": {},
					},
				},
			},
			imageExpectedErr: "`mirrors` cannot be set when `config_path` is provided",
		},
		"deprecated mirrors": {
			config: &PluginConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {},
					},
				},
			},
			imageConfig: &ImageConfig{
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
							Sandboxer: string(ModePodSandbox),
						},
					},
				},
			},
			imageExpected: &ImageConfig{
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
			},
			imageConfig: &ImageConfig{
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
							Sandboxer: string(ModePodSandbox),
						},
					},
				},
			},
			imageExpected: &ImageConfig{
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
	} {
		t.Run(desc, func(t *testing.T) {
			var warnings []deprecation.Warning
			if test.config != nil {
				w, err := ValidatePluginConfig(context.Background(), test.config)
				if test.expectedErr != "" {
					assert.Contains(t, err.Error(), test.expectedErr)
				} else {
					assert.NoError(t, err)
					assert.Equal(t, test.expected, test.config)
				}
				warnings = append(warnings, w...)
			}
			if test.imageConfig != nil {
				w, err := ValidateImageConfig(context.Background(), test.imageConfig)
				if test.imageExpectedErr != "" {
					assert.Contains(t, err.Error(), test.imageExpectedErr)
				} else {
					assert.NoError(t, err)
					assert.Equal(t, test.imageExpected, test.imageConfig)
				}
				warnings = append(warnings, w...)
			}

			if len(test.warnings) > 0 {
				assert.ElementsMatch(t, test.warnings, warnings)
			} else {
				assert.Len(t, warnings, 0)
			}
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := hostAccessingSandbox(tt.config); got != tt.want {
				t.Errorf("hostAccessingSandbox() = %v, want %v", got, tt.want)
			}
		})
	}
}
