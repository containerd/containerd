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

package sbserver

import (
	"testing"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestWindowsHostNetwork(t *testing.T) {
	tests := []struct {
		name     string
		c        *runtime.PodSandboxConfig
		expected bool
	}{
		{
			name: "when host process is false returns false",
			c: &runtime.PodSandboxConfig{
				Windows: &runtime.WindowsPodSandboxConfig{
					SecurityContext: &runtime.WindowsSandboxSecurityContext{
						HostProcess: false,
					},
				},
			},
			expected: false,
		},
		{
			name: "when host process is true return true",
			c: &runtime.PodSandboxConfig{
				Windows: &runtime.WindowsPodSandboxConfig{
					SecurityContext: &runtime.WindowsSandboxSecurityContext{
						HostProcess: true,
					},
				},
			},
			expected: true,
		},
		{
			name: "when no host process return false",
			c: &runtime.PodSandboxConfig{
				Windows: &runtime.WindowsPodSandboxConfig{
					SecurityContext: &runtime.WindowsSandboxSecurityContext{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if hostNetwork(tt.c) != tt.expected {
				t.Errorf("failed hostNetwork got %t expected %t", hostNetwork(tt.c), tt.expected)
			}
		})
	}
}
