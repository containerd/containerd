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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	ctrdsrv "github.com/containerd/containerd/v2/services/server"
	srvconfig "github.com/containerd/containerd/v2/services/server/config"
	"github.com/containerd/log/logtest"
	"github.com/containerd/plugin"
	"github.com/opencontainers/go-digest"

	_ "github.com/containerd/containerd/v2/diff/walking/plugin"
	_ "github.com/containerd/containerd/v2/events/plugin"
	_ "github.com/containerd/containerd/v2/gc/scheduler"
	_ "github.com/containerd/containerd/v2/leases/plugin"
	_ "github.com/containerd/containerd/v2/metadata/plugin"
	_ "github.com/containerd/containerd/v2/runtime/v2"
	_ "github.com/containerd/containerd/v2/runtime/v2/runc/options"
	_ "github.com/containerd/containerd/v2/services/containers"
	_ "github.com/containerd/containerd/v2/services/content"
	_ "github.com/containerd/containerd/v2/services/diff"
	_ "github.com/containerd/containerd/v2/services/events"
	_ "github.com/containerd/containerd/v2/services/images"
	_ "github.com/containerd/containerd/v2/services/introspection"
	_ "github.com/containerd/containerd/v2/services/leases"
	_ "github.com/containerd/containerd/v2/services/namespaces"
	_ "github.com/containerd/containerd/v2/services/snapshots"
	_ "github.com/containerd/containerd/v2/services/tasks"
	_ "github.com/containerd/containerd/v2/services/version"

	"github.com/stretchr/testify/assert"
)

var (
	loadPluginOnce   sync.Once
	loadedPlugins    []plugin.Registration
	loadedPluginsErr error
)

type tweakPluginInitFunc func(t *testing.T, p plugin.Registration) plugin.Registration

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
			initialized,
			map[string]string{
				plugins.PropertyRootDir:  filepath.Join(config.Root, p.URI()),
				plugins.PropertyStateDir: filepath.Join(config.State, p.URI()),
			},
		)

		// load the plugin specific configuration if it is provided
		if p.Config != nil {
			pc, err := config.Decode(ctx, p.URI(), p.Config)
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
		containerd.WithInMemorySandboxControllers(lastInitContext),
	)
	assert.NoError(t, err)

	return client
}

func tweakContentInitFnWithDelayer(commitDelayDuration time.Duration) tweakPluginInitFunc {
	return func(t *testing.T, p plugin.Registration) plugin.Registration {
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
