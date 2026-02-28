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

package opts

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	ostesting "github.com/containerd/containerd/v2/pkg/os/testing"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestMergeGids(t *testing.T) {
	gids1 := []uint32{3, 2, 1}
	gids2 := []uint32{2, 3, 4}
	assert.Equal(t, []uint32{1, 2, 3, 4}, mergeGids(gids1, gids2))
}

func TestRestrictOOMScoreAdj(t *testing.T) {
	current, err := getCurrentOOMScoreAdj()
	require.NoError(t, err)

	got, err := restrictOOMScoreAdj(current - 1)
	require.NoError(t, err)
	assert.Equal(t, got, current)

	got, err = restrictOOMScoreAdj(current)
	require.NoError(t, err)
	assert.Equal(t, got, current)

	got, err = restrictOOMScoreAdj(current + 1)
	require.NoError(t, err)
	assert.Equal(t, got, current+1)
}

func TestWithMountsPrivilegedCgroupOptions(t *testing.T) {
	tests := []struct {
		name          string
		privileged    bool
		hostMountOpts string
		expectedOpts  []string
	}{
		{
			name:          "not privileged, should use default options",
			privileged:    false,
			hostMountOpts: "rw,nosuid,nodev,noexec,relatime,nsdelegate,memory_recursiveprot",
			expectedOpts:  []string{"nosuid", "noexec", "nodev", "relatime", "ro"},
		},
		{
			name:          "privileged with host options present",
			privileged:    true,
			hostMountOpts: "rw,nosuid,nodev,noexec,relatime,nsdelegate,memory_recursiveprot",
			expectedOpts:  []string{"nosuid", "noexec", "nodev", "relatime", "ro", "nsdelegate", "memory_recursiveprot"},
		},
		{
			name:          "privileged with host missing nsdelegate",
			privileged:    true,
			hostMountOpts: "rw,nosuid,nodev,noexec,relatime,memory_recursiveprot",
			expectedOpts:  []string{"nosuid", "noexec", "nodev", "relatime", "ro", "memory_recursiveprot"},
		},
		{
			name:          "privileged with host missing all extra options",
			privileged:    true,
			hostMountOpts: "rw,nosuid,nodev,noexec,relatime",
			expectedOpts:  []string{"nosuid", "noexec", "nodev", "relatime", "ro"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeOS := ostesting.NewFakeOS()
			fakeOS.LookupMountFn = func(path string) (mount.Info, error) {
				if path == "/sys/fs/cgroup" {
					return mount.Info{VFSOptions: tt.hostMountOpts}, nil
				}
				return mount.Info{}, nil
			}

			config := &runtime.ContainerConfig{
				Linux: &runtime.LinuxContainerConfig{
					SecurityContext: &runtime.LinuxContainerSecurityContext{
						Privileged: tt.privileged,
					},
				},
			}

			spec := &runtimespec.Spec{}
			opt := withMounts(fakeOS, config, nil, "", nil, false)
			err := opt(context.Background(), nil, nil, spec)
			require.NoError(t, err)

			var cgroupMount *runtimespec.Mount
			for _, m := range spec.Mounts {
				if m.Destination == "/sys/fs/cgroup" {
					cgroupMount = &m
					break
				}
			}

			require.NotNil(t, cgroupMount)
			assert.ElementsMatch(t, tt.expectedOpts, cgroupMount.Options)
		})
	}
}
