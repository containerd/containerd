//go:build linux

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
	"os"
	"path/filepath"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/defaults"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/continuity/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestGetImageVolumeSnapshotOpts(t *testing.T) {
	ctx := context.Background()

	mappings := []*runtime.IDMapping{
		{
			ContainerId: 0,
			HostId:      65536,
			Length:      65536,
		},
	}

	expectedOpts := optsToInfo(t, containerd.WithRemapperLabels(0, 65536, 0, 65536, 65536))

	for _, test := range []struct {
		name        string
		mount       *runtime.Mount
		expectInfo  *snapshots.Info
		expectError bool
	}{
		{
			name: "with user namespace mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
				UidMappings:   mappings,
				GidMappings:   mappings,
			},
			expectInfo: expectedOpts,
		},
		{
			name: "without mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
			},
		},
		{
			name: "with empty mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
				UidMappings:   []*runtime.IDMapping{},
				GidMappings:   []*runtime.IDMapping{},
			},
		},
		{
			name: "with only UID mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
				UidMappings:   mappings,
			},
		},
		{
			name: "with only GID mappings",
			mount: &runtime.Mount{
				ContainerPath: "/test",
				GidMappings:   mappings,
			},
		},
		{
			name: "with multiple UID mapping lines",
			mount: &runtime.Mount{
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
				GidMappings: mappings,
			},
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

			gotInfo := optsToInfo(t, opts...)
			if test.expectInfo == nil {
				assert.Nil(t, gotInfo)
			} else {
				require.NotNil(t, gotInfo)
				assert.Equal(t, test.expectInfo.Labels, gotInfo.Labels)
			}

			if test.mount.UidMappings != nil {
				assert.NotNil(t, test.mount.UidMappings, "UidMappings should NOT be cleared by getImageVolumeSnapshotOpts")
			}
			if test.mount.GidMappings != nil {
				assert.NotNil(t, test.mount.GidMappings, "GidMappings should NOT be cleared by getImageVolumeSnapshotOpts")
			}
		})
	}
}

func optsToInfo(t *testing.T, opts ...snapshots.Opt) *snapshots.Info {
	t.Helper()
	if len(opts) == 0 {
		return nil
	}
	var info snapshots.Info
	for _, opt := range opts {
		require.NoError(t, opt(&info))
	}
	return &info
}

func TestCleanupImageMountsNested(t *testing.T) {
	testutil.RequiresRoot(t)

	ctx := context.Background()
	c := newTestCRIService()
	c.config.StateDir = t.TempDir()
	const sandboxID = "sandbox"
	require.NoError(t, c.sandboxStore.Add(sandboxstore.NewSandbox(sandboxstore.Metadata{
		ID:     sandboxID,
		Config: &runtime.PodSandboxConfig{},
	}, sandboxstore.Status{})))

	client, err := containerd.New("", containerd.WithServices(containerd.WithSnapshotters(map[string]snapshots.Snapshotter{
		defaults.DefaultSnapshotter: &stubSnapshotter{},
	})))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	c.client = client

	targetBase := c.getImageVolumeBaseDir(sandboxID)
	target := filepath.Join(targetBase, "image")
	rootSource := t.TempDir()
	childSource := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(rootSource, "nested"), 0o755))
	require.NoError(t, os.MkdirAll(target, 0o755))
	t.Cleanup(func() { assert.NoError(t, mount.UnmountRecursive(target, unix.MNT_DETACH)) })
	require.NoError(t, mount.All([]mount.Mount{
		{Type: "bind", Source: rootSource, Options: []string{"bind", "ro"}},
		{Type: "bind", Source: childSource, Target: "nested", Options: []string{"bind", "ro"}},
	}, target))
	require.ErrorIs(t, unix.Unmount(target, 0), unix.EBUSY)

	require.NoError(t, c.cleanupImageMounts(ctx, sandboxID))
	assert.NoDirExists(t, targetBase)
}

type stubSnapshotter struct {
	snapshots.Snapshotter
}

func (*stubSnapshotter) Remove(context.Context, string) error {
	return nil
}
