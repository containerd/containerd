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

package images

import "testing"

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "image ref with dashes preserved",
			input:    "quay.io/calico/kube-controllers:v3.30.6",
			expected: "quay.io/calico/kube-controllers:v3.30.6",
		},
		{
			name:     "manifest digest shortened",
			input:    "manifest-sha256:c6434b5f0e811aabb1f41fd8d1b00a38f25d991c18f52e9a31fa34e4dada0fb7",
			expected: "manifest (c6434b5f0e81)",
		},
		{
			name:     "layer digest shortened",
			input:    "layer-sha256:c6434b5f0e811aabb1f41fd8d1b00a38f25d991c18f52e9a31fa34e4dada0fb7",
			expected: "layer (c6434b5f0e81)",
		},
		{
			name:     "config digest shortened",
			input:    "config-sha256:c6434b5f0e811aabb1f41fd8d1b00a38f25d991c18f52e9a31fa34e4dada0fb7",
			expected: "config (c6434b5f0e81)",
		},
		{
			name:     "plain sha256 digest shortened",
			input:    "sha256:c6434b5f0e811aabb1f41fd8d1b00a38f25d991c18f52e9a31fa34e4dada0fb7",
			expected: "(c6434b5f0e81)",
		},
		{
			name:     "simple name without dashes",
			input:    "nginx:latest",
			expected: "nginx:latest",
		},
		{
			name:     "image ref with multiple dashes",
			input:    "docker.io/my-org/my-app-server:v1.2.3-rc1",
			expected: "docker.io/my-org/my-app-server:v1.2.3-rc1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := displayName(tc.input)
			if got != tc.expected {
				t.Errorf("displayName(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
