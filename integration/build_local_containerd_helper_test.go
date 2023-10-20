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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	ctrdsrv "github.com/containerd/containerd/v2/services/server"
	srvconfig "github.com/containerd/containerd/v2/services/server/config"
	"github.com/containerd/log/logtest"
	"github.com/containerd/plugin"

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

// buildLocalContainerdClient is to return containerd client with initialized
// core plugins in local.
func buildLocalContainerdClient(t *testing.T, tmpDir string) *containerd.Client {
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
