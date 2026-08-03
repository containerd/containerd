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

package nri

import (
	"testing"

	sstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/stretchr/testify/assert"
)

func TestCRIPodSandboxGetIPs(t *testing.T) {
	testCases := []struct {
		name     string
		pod      *criPodSandbox
		expected []string
	}{
		{
			name: "nil Sandbox",
			pod: &criPodSandbox{
				Sandbox: nil,
			},
			expected: nil,
		},
		{
			name: "empty primary IP",
			pod: &criPodSandbox{
				Sandbox: &sstore.Sandbox{
					Metadata: sstore.Metadata{
						IP: "",
					},
				},
			},
			expected: nil,
		},
		{
			name: "single primary IP",
			pod: &criPodSandbox{
				Sandbox: &sstore.Sandbox{
					Metadata: sstore.Metadata{
						IP: "10.0.0.1",
					},
				},
			},
			expected: []string{"10.0.0.1"},
		},
		{
			name: "primary and additional IPs",
			pod: &criPodSandbox{
				Sandbox: &sstore.Sandbox{
					Metadata: sstore.Metadata{
						IP:            "10.0.0.1",
						AdditionalIPs: []string{"fd00::1"},
					},
				},
			},
			expected: []string{"10.0.0.1", "fd00::1"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ips := tc.pod.GetIPs()
			assert.Equal(t, tc.expected, ips)
		})
	}
}
