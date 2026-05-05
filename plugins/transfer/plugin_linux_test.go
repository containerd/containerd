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

package transfer

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	bbolt "go.etcd.io/bbolt"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/plugins"
	ctdlocal "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

// noopSnapshotter satisfies the snapshots.Snapshotter interface with no-op
// methods. It is only used so that metadata.DB returns a non-nil snapshotter,
// matching the production case where the erofs kernel module is loaded but
// mkfs.erofs is absent.
type noopSnapshotter struct{}

func (noopSnapshotter) Stat(_ context.Context, _ string) (snapshots.Info, error) {
	return snapshots.Info{}, nil
}
func (noopSnapshotter) Update(_ context.Context, info snapshots.Info, _ ...string) (snapshots.Info, error) {
	return info, nil
}
func (noopSnapshotter) Usage(_ context.Context, _ string) (snapshots.Usage, error) {
	return snapshots.Usage{}, nil
}
func (noopSnapshotter) Mounts(_ context.Context, _ string) ([]mount.Mount, error) {
	return nil, nil
}
func (noopSnapshotter) Prepare(_ context.Context, _, _ string, _ ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}
func (noopSnapshotter) View(_ context.Context, _, _ string, _ ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}
func (noopSnapshotter) Commit(_ context.Context, _, _ string, _ ...snapshots.Opt) error { return nil }
func (noopSnapshotter) Remove(_ context.Context, _ string) error                        { return nil }
func (noopSnapshotter) Walk(_ context.Context, _ snapshots.WalkFunc, _ ...string) error { return nil }
func (noopSnapshotter) Close() error                                                    { return nil }

// noopApplier satisfies the diff.Applier interface.
type noopApplier struct{}

func (noopApplier) Apply(_ context.Context, _ ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, nil
}

// addPlugin registers a fake plugin into set whose InitFn returns the
// given instance.
func addPlugin(ctx context.Context, t *testing.T, set *plugin.Set, ptype plugin.Type, id string, instance interface{}) {
	t.Helper()
	reg := plugin.Registration{
		Type: ptype,
		ID:   id,
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			return instance, nil
		},
	}
	ic := plugin.NewContext(ctx, set, nil)
	p := reg.Init(ic)
	require.NoError(t, set.Add(p))
}

// addSkippedPlugin registers a fake plugin into set whose InitFn returns
// ErrSkipPlugin, simulating a plugin that failed its availability check.
func addSkippedPlugin(ctx context.Context, t *testing.T, set *plugin.Set, ptype plugin.Type, id string) {
	t.Helper()
	reg := plugin.Registration{
		Type: ptype,
		ID:   id,
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			return nil, fmt.Errorf("binary not found: %w", plugin.ErrSkipPlugin)
		},
	}
	ic := plugin.NewContext(ctx, set, nil)
	p := reg.Init(ic)
	require.NoError(t, set.Add(p))
}

// TestTransferPluginOptionalDifferSkipped verifies that the transfer plugin
// initializes successfully when an optional differ (erofs) is unavailable —
// for example, because mkfs.erofs is not installed on the host. The erofs
// unpack configuration is marked Optional: true in defaultUnpackConfig(), so
// its absence must not block the transfer plugin from loading.
//
// Regression test for https://github.com/containerd/containerd/issues/13346.
func TestTransferPluginOptionalDifferSkipped(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	// Set up a real metadata.DB with both the default and erofs snapshotters
	// registered. Using noopSnapshotter so the metadata.DB returns non-nil for
	// "erofs" — reproducing the case where the erofs kernel module is loaded
	// but mkfs.erofs is absent (erofs snapshotter loads, erofs differ does not).
	bdb, err := bbolt.Open(filepath.Join(tmpDir, "meta.db"), 0600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { bdb.Close() })

	cs, err := ctdlocal.NewStore(filepath.Join(tmpDir, "content"))
	require.NoError(t, err)

	mdb := metadata.NewDB(bdb, cs, map[string]snapshots.Snapshotter{
		defaults.DefaultSnapshotter: noopSnapshotter{},
		"erofs":                     noopSnapshotter{},
	})
	lm := metadata.NewLeaseManager(mdb)

	// Build a minimal plugin set for the transfer plugin's InitFn.
	set := plugin.NewPluginSet()
	addPlugin(ctx, t, set, plugins.MetadataPlugin, "bolt", mdb)
	addPlugin(ctx, t, set, plugins.LeasePlugin, "local", lm)
	// Default differ is present and healthy.
	addPlugin(ctx, t, set, plugins.DiffPlugin, defaults.DefaultDiffer, noopApplier{})
	// Erofs differ is skipped, simulating mkfs.erofs not being installed.
	addSkippedPlugin(ctx, t, set, plugins.DiffPlugin, "erofs")

	// Find the transfer plugin Registration.
	var transferReg *plugin.Registration
	for _, r := range registry.Graph(func(*plugin.Registration) bool { return false }) {
		if r.Type == plugins.TransferPlugin && r.ID == "local" {
			transferReg = &r
			break
		}
	}
	require.NotNil(t, transferReg, "transfer plugin not found in registry")

	ic := plugin.NewContext(ctx, set, nil)
	ic.Config = defaultConfig()

	result := transferReg.Init(ic)
	_, err = result.Instance()
	require.NoError(t, err, "transfer plugin must initialize successfully when optional erofs differ is skipped")
}
