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

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	ctrdsrv "github.com/containerd/containerd/services/server"
	srvconfig "github.com/containerd/containerd/services/server/config"

	_ "github.com/containerd/containerd/diff/walking/plugin"
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
			p,
			initialized,
			config.Root,
			config.State,
		)

		// load the plugin specific configuration if it is provided
		if p.Config != nil {
			pc, err := config.Decode(p)
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
	)
	assert.NoError(t, err)

	return client
}
