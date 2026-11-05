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
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/transfer/local"
	"github.com/containerd/containerd/v2/plugins"
	contentlocal "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/containerd/v2/plugins/services"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"
)

func TestConfigureUnpackPlatforms(t *testing.T) {
	tests := []struct {
		name                  string
		candidates            []*plugin.Registration
		config                transferConfig
		diffServiceSyncFs     *bool
		expectedErr           error
		expectedUnpackConfigs int
		expectedApplier       bool
		expectApplySyncFs     bool
	}{
		{
			name: "optional explicit differ skip",
			candidates: []*plugin.Registration{
				newTestDiffPlugin("erofs", nil, plugin.ErrSkipPlugin, platforms.DefaultSpec()),
			},
			config: transferConfig{
				UnpackConfiguration: []unpackConfiguration{
					{
						Platform:    platforms.Format(platforms.DefaultSpec()),
						Snapshotter: "native",
						Differ:      "erofs",
						Optional:    true,
					},
				},
			},
		},
		{
			name: "required explicit differ skip",
			candidates: []*plugin.Registration{
				newTestDiffPlugin("erofs", nil, plugin.ErrSkipPlugin, platforms.DefaultSpec()),
			},
			config: transferConfig{
				UnpackConfiguration: []unpackConfiguration{
					{
						Platform:    platforms.Format(platforms.DefaultSpec()),
						Snapshotter: "native",
						Differ:      "erofs",
					},
				},
			},
			expectedErr: plugin.ErrSkipPlugin,
		},
		{
			name: "optional explicit differ missing",
			config: transferConfig{
				UnpackConfiguration: []unpackConfiguration{
					{
						Platform:    platforms.Format(platforms.DefaultSpec()),
						Snapshotter: "native",
						Differ:      "missing",
						Optional:    true,
					},
				},
			},
		},
		{
			name: "auto differ skips unavailable candidate",
			candidates: []*plugin.Registration{
				newTestDiffPlugin("skipped", nil, plugin.ErrSkipPlugin, platforms.DefaultSpec()),
				newTestDiffPlugin("usable", testApplier{}, nil, platforms.DefaultSpec()),
			},
			config: transferConfig{
				UnpackConfiguration: []unpackConfiguration{
					{
						Platform:    platforms.Format(platforms.DefaultSpec()),
						Snapshotter: "native",
					},
				},
			},
			expectedUnpackConfigs: 1,
			expectedApplier:       true,
		},
		{
			name: "diff service sync_fs enabled propagates to unpack platforms",
			candidates: []*plugin.Registration{
				newTestDiffPlugin("walking", testApplier{}, nil, platforms.DefaultSpec()),
			},
			config: transferConfig{
				UnpackConfiguration: []unpackConfiguration{
					{
						Platform:    platforms.Format(platforms.DefaultSpec()),
						Snapshotter: "native",
					},
				},
			},
			diffServiceSyncFs:     boolPtr(true),
			expectedUnpackConfigs: 1,
			expectedApplier:       true,
			expectApplySyncFs:     true,
		},
		{
			name: "diff service sync_fs disabled leaves unpack platforms default",
			candidates: []*plugin.Registration{
				newTestDiffPlugin("walking", testApplier{}, nil, platforms.DefaultSpec()),
			},
			config: transferConfig{
				UnpackConfiguration: []unpackConfiguration{
					{
						Platform:    platforms.Format(platforms.DefaultSpec()),
						Snapshotter: "native",
					},
				},
			},
			diffServiceSyncFs:     boolPtr(false),
			expectedUnpackConfigs: 1,
			expectedApplier:       true,
			expectApplySyncFs:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, ic := newTestInitContext(t, map[string]snapshots.Snapshotter{"native": &testSnapshotter{}}, tt.candidates, tt.diffServiceSyncFs)
			lc := &local.TransferConfig{}

			err := configureUnpackPlatforms(ic, ms, &tt.config, lc)
			if !errors.Is(err, tt.expectedErr) {
				t.Fatalf("expected error %v, got %v", tt.expectedErr, err)
			}
			if len(lc.UnpackPlatforms) != tt.expectedUnpackConfigs {
				t.Fatalf("expected %d unpack platforms, got %d", tt.expectedUnpackConfigs, len(lc.UnpackPlatforms))
			}
			if tt.expectedApplier {
				if _, ok := lc.UnpackPlatforms[0].Applier.(testApplier); !ok {
					t.Fatalf("expected test applier, got %T", lc.UnpackPlatforms[0].Applier)
				}
			}
			if tt.expectedUnpackConfigs > 0 {
				var ac diff.ApplyConfig
				for _, opt := range lc.UnpackPlatforms[0].ApplyOpts {
					if err := opt(t.Context(), ocispec.Descriptor{}, &ac); err != nil {
						t.Fatal(err)
					}
				}
				if ac.SyncFs != tt.expectApplySyncFs {
					t.Fatalf("expected unpack ApplyConfig.SyncFs=%v, got %v", tt.expectApplySyncFs, ac.SyncFs)
				}
			}
		})
	}
}

func newTestInitContext(t *testing.T, snapshotters map[string]snapshots.Snapshotter, candidates []*plugin.Registration, diffServiceSyncFs *bool) (*metadata.DB, *plugin.InitContext) {
	t.Helper()

	cs, err := contentlocal.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	bdb, err := bolt.Open(filepath.Join(t.TempDir(), "metadata.db"), 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bdb.Close(); err != nil {
			t.Fatal(err)
		}
	})

	db := metadata.NewDB(bdb, cs, snapshotters)
	ps := plugin.NewPluginSet()
	for name, sn := range snapshotters {
		ic := plugin.NewContext(t.Context(), ps, nil)
		p := (&plugin.Registration{
			Type: plugins.SnapshotPlugin,
			ID:   name,
			InitFn: func(*plugin.InitContext) (any, error) {
				return sn, nil
			},
		}).Init(ic)
		if err := ps.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, candidate := range candidates {
		ic := plugin.NewContext(t.Context(), ps, nil)
		if err := ps.Add(candidate.Init(ic)); err != nil {
			t.Fatal(err)
		}
	}
	if diffServiceSyncFs != nil {
		ic := plugin.NewContext(t.Context(), ps, nil)
		p := (&plugin.Registration{
			Type: plugins.ServicePlugin,
			ID:   services.DiffService,
			InitFn: func(ic *plugin.InitContext) (any, error) {
				ic.Meta.Exports[plugins.DiffServiceSyncFs] = strconv.FormatBool(*diffServiceSyncFs)
				return struct{}{}, nil
			},
		}).Init(ic)
		if err := ps.Add(p); err != nil {
			t.Fatal(err)
		}
	}

	return db, plugin.NewContext(t.Context(), ps, nil)
}

func boolPtr(b bool) *bool {
	return &b
}

func newTestDiffPlugin(id string, applier diff.Applier, err error, supported ...ocispec.Platform) *plugin.Registration {
	return &plugin.Registration{
		Type: plugins.DiffPlugin,
		ID:   id,
		InitFn: func(ic *plugin.InitContext) (any, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, supported...)
			return applier, err
		},
	}
}

type testApplier struct {
	diff.Applier
}

type testSnapshotter struct {
	snapshots.Snapshotter
}
