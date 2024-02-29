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

package v2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/runtime"
	shimbinary "github.com/containerd/containerd/v2/core/runtime/v2/shim"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/cleanup"
	"github.com/containerd/containerd/v2/pkg/events/exchange"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/version"
)

// Config for the v2 runtime
type Config struct {
	// Supported platforms
	Platforms []string `toml:"platforms"`
	// SchedCore enabled linux core scheduling
	SchedCore bool `toml:"sched_core"`
}

func init() {
	// ShimManager is not only for TaskManager,
	// the "shim" sandbox controller also use it to manage shims,
	// so we make it an independent plugin
	registry.Register(&plugin.Registration{
		Type: plugins.ShimPlugin,
		ID:   "shim",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.MetadataPlugin,
		},
		Config: &Config{
			Platforms: defaultPlatforms(),
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config := ic.Config.(*Config)
			supportedPlatforms, err := platforms.ParseAll(config.Platforms)
			if err != nil {
				return nil, err
			}
			ic.Meta.Platforms = supportedPlatforms

			m, err := ic.GetSingle(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ep, err := ic.GetByID(plugins.EventPlugin, "exchange")
			if err != nil {
				return nil, err
			}
			events := ep.(*exchange.Exchange)
			cs := metadata.NewContainerStore(m.(*metadata.DB))
			ss := metadata.NewSandboxStore(m.(*metadata.DB))
			return NewShimManager(&ManagerConfig{
				Address:      ic.Properties[plugins.PropertyGRPCAddress],
				TTRPCAddress: ic.Properties[plugins.PropertyTTRPCAddress],
				Events:       events,
				Store:        cs,
				SchedCore:    config.SchedCore,
				SandboxStore: ss,
			})
		},
		ConfigMigration: func(ctx context.Context, configVersion int, pluginConfigs map[string]interface{}) error {
			if configVersion >= version.ConfigVersion {
				return nil
			}
			const originalPluginName = string(plugins.RuntimePluginV2) + ".task"
			original, ok := pluginConfigs[originalPluginName]
			if !ok {
				return nil
			}
			const newPluginName = string(plugins.ShimPlugin) + ".shim"
			pluginConfigs[originalPluginName] = nil
			pluginConfigs[newPluginName] = original
			return nil
		},
	})
}

type ManagerConfig struct {
	Store        containers.Store
	Events       *exchange.Exchange
	Address      string
	TTRPCAddress string
	SchedCore    bool
	SandboxStore sandbox.Store
}

// NewShimManager creates a manager for v2 shims
func NewShimManager(config *ManagerConfig) (*ShimManager, error) {
	m := &ShimManager{
		containerdAddress:      config.Address,
		containerdTTRPCAddress: config.TTRPCAddress,
		shims:                  runtime.NewNSMap[ShimInstance](),
		events:                 config.Events,
		containers:             config.Store,
		schedCore:              config.SchedCore,
		sandboxStore:           config.SandboxStore,
	}

	return m, nil
}

// ShimManager manages currently running shim processes.
// It is mainly responsible for launching new shims and for proper shutdown and cleanup of existing instances.
// The manager is unaware of the underlying services shim provides and lets higher level services consume them,
// but don't care about lifecycle management.
type ShimManager struct {
	containerdAddress      string
	containerdTTRPCAddress string
	schedCore              bool
	shims                  *runtime.NSMap[ShimInstance]
	events                 *exchange.Exchange
	containers             containers.Store
	// runtimePaths is a cache of `runtime names` -> `resolved fs path`
	runtimePaths sync.Map
	sandboxStore sandbox.Store
}

// ID of the shim manager
func (m *ShimManager) ID() string {
	return plugins.RuntimePluginV2.String() + ".shim"
}

func (m *ShimManager) Create(ctx context.Context, taskID string, bundle *Bundle, opts runtime.CreateOpts) (runtime.Task, error) {
	shim, err := m.Start(ctx, taskID, bundle, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to start shim: %w", err)
	}

	// Cast to shim task and call task service to create a new container task instance.
	// This will not be required once shim service / client implemented.
	shimTask, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	err = shimTask.Create(ctx, bundle.Path, opts)
	if err != nil {
		// NOTE: ctx contains required namespace information.
		m.shims.Delete(ctx, taskID)

		dctx, cancel := timeout.WithContext(cleanup.Background(ctx), cleanupTimeout)
		defer cancel()

		_, errShim := shimTask.delete(dctx, func(context.Context, string) {})
		if errShim != nil {
			if errdefs.IsDeadlineExceeded(errShim) {
				dctx, cancel = timeout.WithContext(cleanup.Background(ctx), cleanupTimeout)
				defer cancel()
			}

			shimTask.Shutdown(dctx)
			shimTask.Close()
		}

		return nil, fmt.Errorf("failed to create shim task: %w", err)
	}

	return shimTask, nil
}

// Start launches a new shim instance
func (m *ShimManager) Start(ctx context.Context, id string, bundle *Bundle, opts runtime.CreateOpts) (_ ShimInstance, retErr error) {
	shim, err := m.startShim(ctx, bundle, id, opts)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			m.cleanupShim(ctx, shim)
		}
	}()

	if err := m.shims.Add(ctx, shim); err != nil {
		return nil, fmt.Errorf("failed to add task: %w", err)
	}

	return shim, nil
}

func (m *ShimManager) startShim(ctx context.Context, bundle *Bundle, id string, opts runtime.CreateOpts) (*shim, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("namespace", ns))

	topts := opts.TaskOptions
	if topts == nil || topts.GetValue() == nil {
		topts = opts.RuntimeOptions
	}

	runtimePath, err := m.resolveRuntimePath(opts.Runtime)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runtime path: %w", err)
	}

	b := shimBinary(bundle, shimBinaryConfig{
		runtime:      runtimePath,
		address:      m.containerdAddress,
		ttrpcAddress: m.containerdTTRPCAddress,
		schedCore:    m.schedCore,
	})
	shim, err := b.Start(ctx, protobuf.FromAny(topts), func() {
		log.G(ctx).WithField("id", id).Info("shim disconnected")

		cleanupAfterDeadShim(cleanup.Background(ctx), id, m.shims, m.events, b)
		// Remove self from the runtime task list. Even though the cleanupAfterDeadShim()
		// would publish taskExit event, but the shim.Delete() would always failed with ttrpc
		// disconnect and there is no chance to remove this dead task from runtime task lists.
		// Thus it's better to delete it here.
		m.shims.Delete(ctx, id)
	})
	if err != nil {
		return nil, fmt.Errorf("start failed: %w", err)
	}

	return shim, nil
}

// restoreBootstrapParams reads bootstrap.json to restore shim configuration.
// If its an old shim, this will perform migration - read address file and write default bootstrap
// configuration (version = 2, protocol = ttrpc, and address).
func restoreBootstrapParams(bundlePath string) (shimbinary.BootstrapParams, error) {
	filePath := filepath.Join(bundlePath, "bootstrap.json")

	// Read bootstrap.json if exists
	if _, err := os.Stat(filePath); err == nil {
		return readBootstrapParams(filePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return shimbinary.BootstrapParams{}, fmt.Errorf("failed to stat %s: %w", filePath, err)
	}

	// File not found, likely its an older shim. Try migrate.

	address, err := shimbinary.ReadAddress(filepath.Join(bundlePath, "address"))
	if err != nil {
		return shimbinary.BootstrapParams{}, fmt.Errorf("unable to migrate shim: failed to get socket address for bundle %s: %w", bundlePath, err)
	}

	params := shimbinary.BootstrapParams{
		Version:  2,
		Address:  address,
		Protocol: "ttrpc",
	}

	if err := writeBootstrapParams(filePath, params); err != nil {
		return shimbinary.BootstrapParams{}, fmt.Errorf("unable to migrate: failed to write bootstrap.json file: %w", err)
	}

	return params, nil
}

func (m *ShimManager) resolveRuntimePath(runtime string) (string, error) {
	if runtime == "" {
		return "", fmt.Errorf("no runtime name")
	}

	// Custom path to runtime binary
	if filepath.IsAbs(runtime) {
		// Make sure it exists before returning ok
		if _, err := os.Stat(runtime); err != nil {
			return "", fmt.Errorf("invalid custom binary path: %w", err)
		}

		return runtime, nil
	}

	// Check if relative path to runtime binary provided
	if strings.Contains(runtime, "/") {
		return "", fmt.Errorf("invalid runtime name %s, correct runtime name should be either format like `io.containerd.runc.v2` or a full path to the binary", runtime)
	}

	// Preserve existing logic and resolve runtime path from runtime name.

	name := shimbinary.BinaryName(runtime)
	if name == "" {
		return "", fmt.Errorf("invalid runtime name %s, correct runtime name should be either format like `io.containerd.runc.v2` or a full path to the binary", runtime)
	}

	if path, ok := m.runtimePaths.Load(name); ok {
		return path.(string), nil
	}

	var (
		cmdPath string
		lerr    error
	)

	binaryPath := shimbinary.BinaryPath(runtime)
	if _, serr := os.Stat(binaryPath); serr == nil {
		cmdPath = binaryPath
	}

	if cmdPath == "" {
		if cmdPath, lerr = exec.LookPath(name); lerr != nil {
			if eerr, ok := lerr.(*exec.Error); ok {
				if eerr.Err == exec.ErrNotFound {
					self, err := os.Executable()
					if err != nil {
						return "", err
					}

					// Match the calling binaries (containerd) path and see
					// if they are side by side. If so, execute the shim
					// found there.
					testPath := filepath.Join(filepath.Dir(self), name)
					if _, serr := os.Stat(testPath); serr == nil {
						cmdPath = testPath
					}
					if cmdPath == "" {
						return "", fmt.Errorf("runtime %q binary not installed %q: %w", runtime, name, os.ErrNotExist)
					}
				}
			}
		}
	}

	cmdPath, err := filepath.Abs(cmdPath)
	if err != nil {
		return "", err
	}

	if path, ok := m.runtimePaths.LoadOrStore(name, cmdPath); ok {
		// We didn't store cmdPath we loaded an already cached value. Use it.
		cmdPath = path.(string)
	}

	return cmdPath, nil
}

// cleanupShim attempts to properly delete and cleanup shim after error
func (m *ShimManager) cleanupShim(ctx context.Context, shim *shim) {
	dctx, cancel := timeout.WithContext(cleanup.Background(ctx), cleanupTimeout)
	defer cancel()

	_ = shim.Delete(dctx)
	m.shims.Delete(dctx, shim.ID())
}

func (m *ShimManager) Get(ctx context.Context, id string) (ShimInstance, error) {
	return m.shims.Get(ctx, id)
}

// Delete a runtime task
func (m *ShimManager) Delete(ctx context.Context, id string) error {
	shim, err := m.shims.Get(ctx, id)
	if err != nil {
		return err
	}

	err = shim.Delete(ctx)
	m.shims.Delete(ctx, id)

	return err
}
