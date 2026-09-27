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

	cstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/nri/pkg/api"
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

func TestCRIContainerGetImage(t *testing.T) {
	const (
		imageName   = "registry.k8s.io/pause:3.10"
		indexDigest = "sha256:ee6521f290b2168b6aa9e6bd8ad0e69d78ed9f6a3c0e5aef8ca9d51876a3f2c1"
		configID    = "sha256:873ed75102791e5b0b8a7fcd41606c92fcec98d56d05ead4ac5131650004c136"
	)

	testCases := []struct {
		name     string
		ctr      *criContainer
		expected *api.Image
	}{
		{
			// meta is always set by nriContainer, but GetImage guards against a
			// nil metadata to avoid a panic.
			name:     "nil metadata",
			ctr:      &criContainer{meta: nil},
			expected: nil,
		},
		{
			// No image identity resolved (e.g. a restored/checkpoint container):
			// return no Image rather than an empty one.
			name:     "no image identity",
			ctr:      &criContainer{meta: &cstore.Metadata{}},
			expected: nil,
		},
		{
			name: "only config digest",
			ctr: &criContainer{meta: &cstore.Metadata{
				ImageRef: configID,
			}},
			expected: &api.Image{
				ConfigDigest: configID,
			},
		},
		{
			name: "full image identity",
			ctr: &criContainer{meta: &cstore.Metadata{
				ImageName:   imageName,
				ImageDigest: indexDigest,
				ImageRef:    configID,
			}},
			expected: &api.Image{
				Name:         imageName,
				Digest:       indexDigest,
				ConfigDigest: configID,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.ctr.GetImage())
		})
	}
}
