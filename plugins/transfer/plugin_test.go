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
	"testing"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/transfer/local"
	"github.com/containerd/containerd/v2/plugins"
	contentlocal "github.com/containerd/containerd/v2/plugins/content/local"
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
		expectedErr           error
		expectedUnpackConfigs int
		expectedApplier       bool
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, ic := newTestInitContext(t, map[string]snapshots.Snapshotter{"native": &testSnapshotter{}}, tt.candidates)
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
		})
	}
}

func newTestInitContext(t *testing.T, snapshotters map[string]snapshots.Snapshotter, candidates []*plugin.Registration) (*metadata.DB, *plugin.InitContext) {
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

	return db, plugin.NewContext(t.Context(), ps, nil)
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
