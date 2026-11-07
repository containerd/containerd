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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/snapshots"
	ctransfer "github.com/containerd/containerd/v2/core/transfer"
	"github.com/containerd/containerd/v2/core/transfer/image"
	"github.com/containerd/containerd/v2/core/transfer/local"
	tregistry "github.com/containerd/containerd/v2/core/transfer/registry"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins"
	contentlocal "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestTransferDescriptorURLMetric(t *testing.T) {
	ctx := namespaces.WithNamespace(t.Context(), "default")

	layerBytes := []byte("foreign layer payload")
	layerDigest := digest.FromBytes(layerBytes)

	configBytes := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["` + layerDigest.String() + `"]}}`)
	configDigest := digest.FromBytes(configBytes)

	externalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(layerBytes)))
		_, _ = w.Write(layerBytes)
	}))
	t.Cleanup(externalServer.Close)

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    configDigest,
			Size:      int64(len(configBytes)),
		},
		Layers: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    layerDigest,
				Size:      int64(len(layerBytes)),
				URLs:      []string{externalServer.URL + "/layer"},
			},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestDigest := digest.FromBytes(manifestBytes)

	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/test/image/manifests/latest", "/v2/test/image/manifests/" + manifestDigest.String():
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(manifestBytes)))
			if r.Method == http.MethodHead {
				return
			}
			_, _ = w.Write(manifestBytes)
		case "/v2/test/image/blobs/" + configDigest.String():
			w.Header().Set("Content-Length", strconv.Itoa(len(configBytes)))
			_, _ = w.Write(configBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(registryServer.Close)

	db, _ := newTestInitContext(t, nil, nil)
	require.NoError(t, db.Init(ctx))
	ps := plugin.NewPluginSet()
	require.NoError(t, ps.Add((&plugin.Registration{
		Type: plugins.MetadataPlugin,
		ID:   "bolt",
		InitFn: func(*plugin.InitContext) (any, error) {
			return db, nil
		},
	}).Init(plugin.NewContext(ctx, ps, nil))))
	require.NoError(t, ps.Add((&plugin.Registration{
		Type: plugins.LeasePlugin,
		ID:   "manager",
		InitFn: func(*plugin.InitContext) (any, error) {
			return metadata.NewLeaseManager(db), nil
		},
	}).Init(plugin.NewContext(ctx, ps, nil))))

	var transferReg *plugin.Registration
	for _, r := range registry.Graph(func(*plugin.Registration) bool { return false }) {
		if r.Type == plugins.TransferPlugin && r.ID == "local" {
			transferReg = &r
			break
		}
	}
	require.NotNil(t, transferReg)

	ic := plugin.NewContext(ctx, ps, nil)
	cfg := defaultConfig()
	cfg.UnpackConfiguration = []unpackConfiguration{}
	ic.Config = cfg
	initialized := transferReg.Init(ic)
	inst, err := initialized.Instance()
	require.NoError(t, err)
	transferrer := inst.(ctransfer.Transferrer)

	before := descriptorURLMetricValue(t)
	ref := strings.TrimPrefix(registryServer.URL, "http://") + "/test/image:latest"
	ociReg, err := tregistry.NewOCIRegistry(ctx, ref, tregistry.WithDefaultScheme("http"))
	require.NoError(t, err)

	assert.NoError(t, transferrer.Transfer(ctx, ociReg, image.NewStore(ref)))
	assert.Equal(t, before+1, descriptorURLMetricValue(t))
}

func descriptorURLMetricValue(t *testing.T) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() == "containerd_transfer_descriptor_url_requests_total" {
			for _, m := range f.Metric {
				if m.GetCounter() != nil {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
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
