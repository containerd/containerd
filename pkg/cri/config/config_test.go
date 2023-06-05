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
)

func TestValidateConfig(t *testing.T) {
	for desc, test := range map[string]struct {
		config      *PluginConfig
		expectedErr string
		expected    *PluginConfig
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
							Sandboxer: string(ModePodSandbox),
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
			err := ValidatePluginConfig(context.Background(), test.config)
			if test.expectedErr != "" {
				assert.Contains(t, err.Error(), test.expectedErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, test.config)
			}
		})
	}
}
