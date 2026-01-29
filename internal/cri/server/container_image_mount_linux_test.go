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

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestGetImageVolumeSnapshotOpts(t *testing.T) {
	ctx := context.Background()

	for _, test := range []struct {
		name        string
		mount       *runtime.Mount
		expectOpts  bool
		expectError bool
	}{
		{
			name: "with user namespace mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
				UidMappings: []*runtime.IDMapping{
					{
						ContainerId: 0,
						HostId:      65536,
						Length:      65536,
					},
				},
				GidMappings: []*runtime.IDMapping{
					{
						ContainerId: 0,
						HostId:      65536,
						Length:      65536,
					},
				},
			},
			expectOpts:  true,
			expectError: false,
		},
		{
			name: "without mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
			},
			expectOpts:  false,
			expectError: false,
		},
		{
			name: "with empty mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
				UidMappings:   []*runtime.IDMapping{},
				GidMappings:   []*runtime.IDMapping{},
			},
			expectOpts:  false,
			expectError: false,
		},
		{
			name: "with only UID mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
				UidMappings: []*runtime.IDMapping{
					{
						ContainerId: 0,
						HostId:      65536,
						Length:      65536,
					},
				},
			},
			expectOpts:  false,
			expectError: true,
		},
		{
			name: "with only GID mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
				GidMappings: []*runtime.IDMapping{
					{
						ContainerId: 0,
						HostId:      65536,
						Length:      65536,
					},
				},
			},
			expectOpts:  false,
			expectError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := &criService{}

			opts, err := c.getImageVolumeSnapshotOpts(ctx, test.mount)

			if test.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if test.expectOpts {
				assert.NotEmpty(t, opts, "expected snapshot options to be returned")
			} else {
				assert.Empty(t, opts, "expected no snapshot options")
			}
		})
	}
}

func TestGetImageVolumeSnapshotOpts_InvalidMappings(t *testing.T) {
	ctx := context.Background()

	// Test with invalid mappings (multiple mapping lines)
	mount := &runtime.Mount{
		ContainerPath: "/test",
		UidMappings: []*runtime.IDMapping{
			{
				ContainerId: 0,
				HostId:      65536,
				Length:      65536,
			},
			{
				ContainerId: 65536,
				HostId:      131072,
				Length:      65536,
			},
		},
		GidMappings: []*runtime.IDMapping{
			{
				ContainerId: 0,
				HostId:      65536,
				Length:      65536,
			},
		},
	}

	c := &criService{}

	_, err := c.getImageVolumeSnapshotOpts(ctx, mount)
	assert.Error(t, err, "expected error with invalid mappings")
	assert.Contains(t, err.Error(), "failed to parse UID mappings", "error should mention UID mappings failure")
}
