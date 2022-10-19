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
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/timeout"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/containerd/runtime"
	shimbinary "github.com/containerd/containerd/runtime/v2/shim"
	"github.com/containerd/ttrpc"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Config for the v2 runtime
type Config struct {
	// Supported platforms
	Platforms []string `toml:"platforms"`
	// SchedCore enabled linux core scheduling
	SchedCore bool `toml:"sched_core"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.RuntimePluginV2,
		ID:   "task",
		Requires: []plugin.Type{
			plugin.EventPlugin,
			plugin.MetadataPlugin,
			plugin.TaskPlugin,
		},
		Config: &Config{
			Platforms: defaultPlatforms(),
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config := ic.Config.(*Config)
			supportedPlatforms, err := parsePlatforms(config.Platforms)
			if err != nil {
				return nil, err
			}

			ic.Meta.Platforms = supportedPlatforms

			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ep, err := ic.GetByID(plugin.EventPlugin, "exchange")
			if err != nil {
				return nil, err
			}
			cs := metadata.NewContainerStore(m.(*metadata.DB))
			events := ep.(*exchange.Exchange)

			shimManager, err := NewShimManager(ic.Context, &ManagerConfig{
				Root:         ic.Root,
				State:        ic.State,
				Address:      ic.Address,
				TTRPCAddress: ic.TTRPCAddress,
				Events:       events,
				Store:        cs,
				SchedCore:    config.SchedCore,
			})
			if err != nil {
				return nil, err
			}

			return NewTaskManager(shimManager), nil
		},
	})

	// Task manager uses shim manager as a dependency to manage shim instances.
	// However, due to time limits and to avoid migration steps in 1.6 release,
	// use the following workaround.
	// This expected to be removed in 1.7.
	plugin.Register(&plugin.Registration{
		Type: plugin.RuntimePluginV2,
		ID:   "shim",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			taskManagerI, err := ic.GetByID(plugin.RuntimePluginV2, "task")
			if err != nil {
				return nil, err
			}

			taskManager := taskManagerI.(*TaskManager)
			return taskManager.manager, nil
		},
	})
}

type ManagerConfig struct {
	Root         string
	State        string
	Store        containers.Store
	Events       *exchange.Exchange
	Address      string
	TTRPCAddress string
	SchedCore    bool
}

// NewShimManager creates a manager for v2 shims
func NewShimManager(ctx context.Context, config *ManagerConfig) (*ShimManager, error) {
	for _, d := range []string{config.Root, config.State} {
		if err := os.MkdirAll(d, 0711); err != nil {
			return nil, err
		}
	}

	m := &ShimManager{
		root:                   config.Root,
		state:                  config.State,
		containerdAddress:      config.Address,
		containerdTTRPCAddress: config.TTRPCAddress,
		shims:                  runtime.NewNSMap[ShimInstance](),
		events:                 config.Events,
		containers:             config.Store,
		schedCore:              config.SchedCore,
	}

	if err := m.loadExistingTasks(ctx); err != nil {
		return nil, err
	}

	return m, nil
}

// ShimManager manages currently running shim processes.
// It is mainly responsible for launching new shims and for proper shutdown and cleanup of existing instances.
// The manager is unaware of the underlying services shim provides and lets higher level services consume them,
// but don't care about lifecycle management.
type ShimManager struct {
	root                   string
	state                  string
	containerdAddress      string
	containerdTTRPCAddress string
	schedCore              bool
	shims                  *runtime.NSMap[ShimInstance]
	events                 *exchange.Exchange
	containers             containers.Store
	// runtimePaths is a cache of `runtime names` -> `resolved fs path`
	runtimePaths sync.Map
}

// ID of the shim manager
func (m *ShimManager) ID() string {
	return fmt.Sprintf("%s.%s", plugin.RuntimePluginV2, "shim")
}

// Start launches a new shim instance
func (m *ShimManager) Start(ctx context.Context, id string, opts runtime.CreateOpts) (_ ShimInstance, retErr error) {
	if opts.TaskAddress != "" {
		log.G(ctx).Infof("start task in sandbox address %s, bundle: %s", opts.TaskAddress, opts.Bundle)
		var bundle *Bundle
		ns, err := namespaces.NamespaceRequired(ctx)
		if err != nil {
			return nil, err
		}
		if opts.Bundle != "" {
			bundle = &Bundle{
				ID:        id,
				Path:      opts.Bundle,
				Namespace: ns,
			}
			if err := os.Symlink(opts.Bundle, filepath.Join(m.state, ns, id)); err != nil {
				return nil, err
			}
		} else {
			bundle, err = NewBundle(ctx, m.root, m.state, id, opts.Spec)
			if err != nil {
				return nil, err
			}
			defer func() {
				if retErr != nil {
					bundle.Delete()
				}
			}()
		}

		address := opts.TaskAddress
		log.G(ctx).Infof("connect to shimv2 server in sandbox with address %s", address)
		var conn net.Conn
		var e error
		for i := 0; i < 500; i++ {
			conn, e = shimbinary.Connect(address, shimbinary.AnonDialer)
			if e != nil {
				time.Sleep(10 * time.Millisecond)
			} else {
				break
			}
		}
		if e != nil {
			return nil, e
		}

		log.G(ctx).Infof("succeed establish the connection to %s, new ttrpc client", address)
		c := ttrpc.NewClient(conn, ttrpc.WithOnClose(func() {
			log.G(ctx).WithField("id", id).Info("shim disconnected")

			if err := bundle.Delete(); err != nil {
				log.G(ctx).WithField("id", id).Errorf("failed to delete bundle")
			}
			// Remove self from the runtime task list. Even though the cleanupAfterDeadShim()
			// would publish taskExit event, but the shim.Delete() would always failed with ttrpc
			// disconnect and there is no chance to remove this dead task from runtime task lists.
			// Thus it's better to delete it here.
			m.shims.Delete(ctx, id)
		}))
		shim := &shim{
			bundle: bundle,
			client: c,
		}
		defer func() {
			if err != nil {
				dctx, cancel := timeout.WithContext(context.Background(), cleanupTimeout)
				defer cancel()
				err := shim.Delete(dctx)
				if err != nil {
					log.G(ctx).WithField("namespace", ns).WithError(err).Error("failed to delete shim")
					return
				}
			}
		}()

		addressPath := filepath.Join(bundle.Path, "address")
		if err := shimbinary.WriteAddress(addressPath, address); err != nil {
			return nil, err
		}

		if err := m.shims.Add(ctx, shim); err != nil {
			return nil, fmt.Errorf("failed to add task: %w", err)
		}
		return shim, nil
	}
	bundle, err := NewBundle(ctx, m.root, m.state, id, opts.Spec)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			bundle.Delete()
		}
	}()

	shim, err := m.startShim(ctx, bundle, id, opts)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			m.cleanupShim(shim)
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

		cleanupAfterDeadShim(context.Background(), id, ns, m.shims, m.events, b)
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
		return "", fmt.Errorf("invalid runtime name %s, correct runtime name should be either format like `io.containerd.runc.v1` or a full path to the binary", runtime)
	}

	// Preserve existing logic and resolve runtime path from runtime name.

	name := shimbinary.BinaryName(runtime)
	if name == "" {
		return "", fmt.Errorf("invalid runtime name %s, correct runtime name should be either format like `io.containerd.runc.v1` or a full path to the binary", runtime)
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
func (m *ShimManager) cleanupShim(shim *shim) {
	dctx, cancel := timeout.WithContext(context.Background(), cleanupTimeout)
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

func parsePlatforms(platformStr []string) ([]ocispec.Platform, error) {
	p := make([]ocispec.Platform, len(platformStr))
	for i, v := range platformStr {
		parsed, err := platforms.Parse(v)
		if err != nil {
			return nil, err
		}
		p[i] = parsed
	}
	return p, nil
}

// TaskManager wraps task service client on top of shim manager.
type TaskManager struct {
	manager *ShimManager
}

// NewTaskManager creates a new task manager instance.
func NewTaskManager(shims *ShimManager) *TaskManager {
	return &TaskManager{
		manager: shims,
	}
}

// ID of the task manager
func (m *TaskManager) ID() string {
	return fmt.Sprintf("%s.%s", plugin.RuntimePluginV2, "task")
}

// Create launches new shim instance and creates new task
func (m *TaskManager) Create(ctx context.Context, taskID string, opts runtime.CreateOpts) (runtime.Task, error) {
	shim, err := m.manager.Start(ctx, taskID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to start shim: %w", err)
	}

	// Cast to shim task and call task service to create a new container task instance.
	// This will not be required once shim service / client implemented.
	shimTask := newShimTask(shim)
	t, err := shimTask.Create(ctx, opts)
	if err != nil {
		// NOTE: ctx contains required namespace information.
		m.manager.shims.Delete(ctx, taskID)

		dctx, cancel := timeout.WithContext(context.Background(), cleanupTimeout)
		defer cancel()

		sandboxed := opts.TaskAddress != ""
		_, errShim := shimTask.delete(dctx, sandboxed, func(context.Context, string) {})
		if errShim != nil {
			if errdefs.IsDeadlineExceeded(errShim) {
				dctx, cancel = timeout.WithContext(context.Background(), cleanupTimeout)
				defer cancel()
			}

			shimTask.Shutdown(dctx)
			shimTask.Client().Close()
		}

		return nil, fmt.Errorf("failed to create shim task: %w", err)
	}

	return t, nil
}

// Get a specific task
func (m *TaskManager) Get(ctx context.Context, id string) (runtime.Task, error) {
	shim, err := m.manager.shims.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return newShimTask(shim), nil
}

// Tasks lists all tasks
func (m *TaskManager) Tasks(ctx context.Context, all bool) ([]runtime.Task, error) {
	shims, err := m.manager.shims.GetAll(ctx, all)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Task, len(shims))
	for i := range shims {
		out[i] = newShimTask(shims[i])
	}
	return out, nil
}

// Delete deletes the task and shim instance
func (m *TaskManager) Delete(ctx context.Context, taskID string) (*runtime.Exit, error) {
	shim, err := m.manager.shims.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	container, err := m.manager.containers.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	var (
		sandboxed = container.SandboxKey != ""
		shimTask  = newShimTask(shim)
	)

	exit, err := shimTask.delete(ctx, sandboxed, func(ctx context.Context, id string) {
		m.manager.shims.Delete(ctx, id)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to delete task: %w", err)
	}

	return exit, nil
}
