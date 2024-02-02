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

	kernel "github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/stretchr/testify/assert"
)

func TestValidateEnableUnprivileged(t *testing.T) {
	origKernelGreaterEqualThan := kernelGreaterEqualThan
	t.Cleanup(func() {
		kernelGreaterEqualThan = origKernelGreaterEqualThan
	})

	tests := []struct {
		name          string
		config        *RuntimeConfig
		kernelGreater bool
		expectedErr   string
	}{
		{
			name: "disable unprivileged_icmp and unprivileged_port",
			config: &RuntimeConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
				EnableUnprivilegedICMP:  false,
				EnableUnprivilegedPorts: false,
			},
			expectedErr: "",
		},
		{
			name: "enable unprivileged_icmp or unprivileged_port, but kernel version is smaller than 4.11",
			config: &RuntimeConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
				EnableUnprivilegedICMP:  true,
				EnableUnprivilegedPorts: true,
			},
			kernelGreater: false,
			expectedErr:   "unprivileged_icmp and unprivileged_port require kernel version greater than or equal to 4.11",
		},
		{
			name: "enable unprivileged_icmp or unprivileged_port, but kernel version is greater than or equal 4.11",
			config: &RuntimeConfig{
				ContainerdConfig: ContainerdConfig{
					DefaultRuntimeName: RuntimeDefault,
					Runtimes: map[string]Runtime{
						RuntimeDefault: {
							Type: "default",
						},
					},
				},
				EnableUnprivilegedICMP:  true,
				EnableUnprivilegedPorts: true,
			},
			kernelGreater: true,
			expectedErr:   "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kernelGreaterEqualThan = func(minVersion kernel.KernelVersion) (bool, error) {
				return test.kernelGreater, nil
			}
			err := ValidateEnableUnprivileged(context.Background(), test.config)
			if test.expectedErr != "" {
				assert.Equal(t, err.Error(), test.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
