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

package integration

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/plugin"
	ctrdsrv "github.com/containerd/containerd/services/server"
	srvconfig "github.com/containerd/containerd/services/server/config"
	"github.com/containerd/log/logtest"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"

	_ "github.com/containerd/containerd/diff/walking/plugin"
	"github.com/containerd/containerd/events/exchange"
	_ "github.com/containerd/containerd/events/plugin"
	_ "github.com/containerd/containerd/gc/scheduler"
	_ "github.com/containerd/containerd/leases/plugin"
	_ "github.com/containerd/containerd/metadata/plugin"
	_ "github.com/containerd/containerd/runtime/v2"
	_ "github.com/containerd/containerd/runtime/v2/runc/options"
	_ "github.com/containerd/containerd/services/containers"
	_ "github.com/containerd/containerd/services/content"
	_ "github.com/containerd/containerd/services/diff"
	_ "github.com/containerd/containerd/services/events"
	_ "github.com/containerd/containerd/services/images"
	_ "github.com/containerd/containerd/services/introspection"
	_ "github.com/containerd/containerd/services/leases"
	_ "github.com/containerd/containerd/services/namespaces"
	_ "github.com/containerd/containerd/services/snapshots"
	_ "github.com/containerd/containerd/services/tasks"
	_ "github.com/containerd/containerd/services/version"

	"github.com/stretchr/testify/assert"
)

var (
	loadPluginOnce   sync.Once
	loadedPlugins    []*plugin.Registration
	loadedPluginsErr error
)

type tweakPluginInitFunc func(t *testing.T, p *plugin.Registration) *plugin.Registration

// buildLocalContainerdClient is to return containerd client with initialized
// core plugins in local.
func buildLocalContainerdClient(t *testing.T, tmpDir string, tweakInitFn tweakPluginInitFunc) *containerd.Client {
	ctx := logtest.WithT(context.Background(), t)

	// load plugins
	loadPluginOnce.Do(func() {
		loadedPlugins, loadedPluginsErr = ctrdsrv.LoadPlugins(ctx, &srvconfig.Config{})
		assert.NoError(t, loadedPluginsErr)
	})

	// init plugins
	var (
		// TODO: Remove this in 2.0 and let event plugin crease it
		events = exchange.NewExchange()

		initialized = plugin.NewPluginSet()

		// NOTE: plugin.Set doesn't provide the way to get all the same
		// type plugins. lastInitContext is used to record the last
		// initContext and work with getServicesOpts.
		lastInitContext *plugin.InitContext

		config = &srvconfig.Config{
			Version: 2,
			Root:    filepath.Join(tmpDir, "root"),
			State:   filepath.Join(tmpDir, "state"),
		}
	)

	for _, p := range loadedPlugins {
		initContext := plugin.NewContext(
			ctx,
			p,
			initialized,
			config.Root,
			config.State,
		)
		initContext.Events = events

		// load the plugin specific configuration if it is provided
		if p.Config != nil {
			pc, err := config.Decode(p)
			assert.NoError(t, err)

			initContext.Config = pc
		}

		if tweakInitFn != nil {
			p = tweakInitFn(t, p)
		}

		result := p.Init(initContext)
		assert.NoError(t, initialized.Add(result))

		_, err := result.Instance()
		assert.NoError(t, err)

		lastInitContext = initContext
	}

	client, err := containerd.New(
		"",
		containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
		containerd.WithDefaultPlatform(platforms.Default()),
		containerd.WithInMemoryServices(lastInitContext),
	)
	assert.NoError(t, err)

	return client
}

func tweakContentInitFnWithDelayer(commitDelayDuration time.Duration) tweakPluginInitFunc {
	return func(t *testing.T, p *plugin.Registration) *plugin.Registration {
		if p.URI() != "io.containerd.content.v1.content" {
			return p
		}

		oldInitFn := p.InitFn
		p.InitFn = func(ic *plugin.InitContext) (interface{}, error) {
			instance, err := oldInitFn(ic)
			if err != nil {
				return nil, err
			}

			return &contentStoreDelayer{
				t: t,

				Store:               instance.(content.Store),
				commitDelayDuration: commitDelayDuration,
			}, nil
		}
		return p
	}
}

type contentStoreDelayer struct {
	t *testing.T

	content.Store
	commitDelayDuration time.Duration
}

func (cs *contentStoreDelayer) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	w, err := cs.Store.Writer(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &contentWriterDelayer{
		t: cs.t,

		Writer:              w,
		commitDelayDuration: cs.commitDelayDuration,
	}, nil
}

type contentWriterDelayer struct {
	t *testing.T

	content.Writer
	commitDelayDuration time.Duration
}

func (w *contentWriterDelayer) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	w.t.Logf("[testcase: %s] Commit %v blob after %v", w.t.Name(), expected, w.commitDelayDuration)
	time.Sleep(w.commitDelayDuration)
	return w.Writer.Commit(ctx, size, expected, opts...)
}
