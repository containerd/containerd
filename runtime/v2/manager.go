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

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/events/exchange"
	"github.com/containerd/containerd/v2/metadata"
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/pkg/cleanup"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/runtime"
	shimbinary "github.com/containerd/containerd/v2/runtime/v2/shim"
)

// Config for the v2 runtime
type Config struct {
	// Supported platforms
	Platforms []string `toml:"platforms"`
	// SchedCore enabled linux core scheduling
	SchedCore bool `toml:"sched_core"`
}

func init() {
	// Task manager uses shim manager as a dependency to manage shim instances.
	// However, due to time limits and to avoid migration steps in 1.6 release,
	// use the following workaround.
	// This expected to be removed in 1.7.
	registry.Register(&plugin.Registration{
		Type: plugins.ShimPlugin,
		ID:   "shim",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
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
			return NewShimManager(&ManagerConfig{
				Address:      ic.Properties[plugins.PropertyGRPCAddress],
				TTRPCAddress: ic.Properties[plugins.PropertyTTRPCAddress],
				Events:       events,
				Store:        cs,
			})
		},
	})

	registry.Register(&plugin.Registration{
		Type: plugins.RuntimePluginV2,
		ID:   "task",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.MetadataPlugin,
			plugins.ShimPlugin,
			plugins.SandboxControllerPlugin,
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
			ss := metadata.NewSandboxStore(m.(*metadata.DB))

			client, err := containerd.New(
				"",
				containerd.WithServices(containerd.WithSandboxStore(ss)),
				containerd.WithInMemorySandboxControllers(ic),
			)
			if err != nil {
				return nil, err
			}
			sandboxedTaskManager := NewSandboxedTaskManager(client)

			shimManagerI, err := ic.GetByID(plugins.ShimPlugin, "shim")
			if err != nil {
				return nil, err
			}
			shimManager := shimManagerI.(*ShimManager)
			if err != nil {
				return nil, err
			}

			root, state := ic.Properties[plugins.PropertyRootDir], ic.Properties[plugins.PropertyStateDir]
			for _, d := range []string{root, state} {
				if err := os.MkdirAll(d, 0711); err != nil {
					return nil, err
				}
			}
			return NewTaskManager(ic.Context, root, state, shimManager, sandboxedTaskManager)
		},
	})
}

type ManagerConfig struct {
	Store        containers.Store
	Events       *exchange.Exchange
	Address      string
	TTRPCAddress string
	SchedCore    bool
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
}

// ID of the shim manager
func (m *ShimManager) ID() string {
	return plugins.RuntimePluginV2.String() + ".shim"
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

func (m *ShimManager) Load(ctx context.Context, bundle *Bundle) error {
	var (
		runtime string
		id      = bundle.ID
	)

	// If we're on 1.6+ and specified custom path to the runtime binary, path will be saved in 'shim-binary-path' file.
	if data, err := os.ReadFile(filepath.Join(bundle.Path, "shim-binary-path")); err == nil {
		runtime = string(data)
	} else if err != nil && !os.IsNotExist(err) {
		log.G(ctx).WithError(err).Error("failed to read `runtime` path from bundle")
	}

	// Query runtime name from metadata store
	if runtime == "" {
		container, err := m.containers.Get(ctx, id)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("loading container %s", id)
			if umountErr := mount.UnmountRecursive(filepath.Join(bundle.Path, "rootfs"), 0); umountErr != nil {
				log.G(ctx).WithError(umountErr).Errorf("failed to unmount of rootfs %s", id)
			}
			return err
		}
		runtime = container.Runtime.Name
	}

	runtime, err := m.resolveRuntimePath(runtime)
	if err != nil {
		return fmt.Errorf("failed to resolve runtime path: %w", err)
	}

	binaryCall := shimBinary(bundle,
		shimBinaryConfig{
			runtime:      runtime,
			address:      m.containerdAddress,
			ttrpcAddress: m.containerdTTRPCAddress,
			schedCore:    m.schedCore,
		})
	shim, err := loadShimTask(ctx, bundle, func() {
		log.G(ctx).WithField("id", id).Info("shim disconnected")

		cleanupAfterDeadShim(cleanup.Background(ctx), id, m.shims, m.events, binaryCall)
		// Remove self from the runtime task list.
		m.shims.Delete(ctx, id)
	})
	if err != nil {
		cleanupAfterDeadShim(ctx, id, m.shims, m.events, binaryCall)
		return fmt.Errorf("unable to load shim %q: %w", id, err)
	}

	// There are 2 possibilities for the loaded shim here:
	// 1. It could be a shim that is running a task.
	// 3. Or it could be a shim that was created for running a task but
	// something happened (probably a containerd crash) and the task was never
	// created. This shim process should be cleaned up here. Look at
	// containerd/containerd#6860 for further details.
	pInfo, pidErr := shim.Pids(ctx)
	if len(pInfo) == 0 || errors.Is(pidErr, errdefs.ErrNotFound) {
		log.G(ctx).WithField("id", id).Info("cleaning leaked shim process")
		// We are unable to get Pids from the shim, we should clean it up here.
		// No need to do anything for removeTask since we never added this shim.
		shim.delete(ctx, func(ctx context.Context, id string) {})
	} else {
		m.shims.Add(ctx, shim.ShimInstance)
	}
	return nil
}

func (m *ShimManager) LoadShim(ctx context.Context, bundle *Bundle, onClose func()) error {
	shim, err := loadShim(ctx, bundle, onClose)
	if err != nil {
		return err
	}
	return m.shims.Add(ctx, shim)
}

// TaskManager wraps task service client on top of shim manager.
type TaskManager struct {
	root                 string
	state                string
	shimManager          *ShimManager
	sandboxedTaskManager *SandboxedTaskManager
}

// NewTaskManager creates a new task manager instance.
func NewTaskManager(ctx context.Context, root, state string, shims *ShimManager, sandboxes *SandboxedTaskManager) (*TaskManager, error) {
	m := &TaskManager{
		root:                 root,
		state:                state,
		shimManager:          shims,
		sandboxedTaskManager: sandboxes,
	}

	if err := m.loadExistingTasks(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// ID of the task manager
func (m *TaskManager) ID() string {
	return plugins.RuntimePluginV2.String() + ".task"
}

// Create launches new shim instance and creates new task
func (m *TaskManager) Create(ctx context.Context, taskID string, opts runtime.CreateOpts) (t runtime.Task, retErr error) {
	bundle, err := NewBundle(ctx, m.root, m.state, taskID, opts.Spec)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			bundle.Delete()
		}
	}()

	if len(opts.SandboxID) > 0 {
		// if err==ErrCanNotHandle, we should fallback to the shim
		if t, err := m.sandboxedTaskManager.Create(ctx, taskID, bundle, opts); err != ErrCanNotHandle {
			return t, err
		}
	}

	shim, err := m.shimManager.Start(ctx, taskID, bundle, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to start shim: %w", err)
	}

	// Cast to shim task and call task service to create a new container task instance.
	// This will not be required once shim service / client implemented.
	shimTask, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	task, err := shimTask.Create(ctx, opts)
	if err != nil {
		// NOTE: ctx contains required namespace information.
		m.shimManager.shims.Delete(ctx, taskID)

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

	return task, nil
}

// Get a specific task
func (m *TaskManager) Get(ctx context.Context, id string) (runtime.Task, error) {
	t, err := m.sandboxedTaskManager.Get(ctx, id)
	if errdefs.IsNotFound(err) {
		shim, shimErr := m.shimManager.shims.Get(ctx, id)
		if shimErr != nil {
			return nil, shimErr
		}
		return newShimTask(shim)
	}
	return t, err
}

// Tasks lists all tasks
func (m *TaskManager) Tasks(ctx context.Context, all bool) ([]runtime.Task, error) {
	var tasks []runtime.Task
	sts, err := m.sandboxedTaskManager.GetAll(ctx, all)
	if err != nil {
		return nil, err
	}
	tasks = append(tasks, sts...)

	shims, err := m.shimManager.shims.GetAll(ctx, all)
	if err != nil {
		return nil, err
	}

	for _, s := range shims {
		st, err := newShimTask(s)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, st)
	}
	return tasks, nil
}

// Delete deletes the task and shim instance
func (m *TaskManager) Delete(ctx context.Context, taskID string) (*runtime.Exit, error) {
	exit, err := m.sandboxedTaskManager.Delete(ctx, taskID)
	if err == nil {
		return exit, nil
	}
	if !errdefs.IsNotFound(err) {
		return nil, err
	}

	shim, err := m.shimManager.shims.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}

	shimTask, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	exit, err = shimTask.delete(ctx, func(ctx context.Context, id string) {
		m.shimManager.shims.Delete(ctx, id)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to delete task: %w", err)
	}

	return exit, nil
}

func (m *TaskManager) loadExistingTasks(ctx context.Context) error {
	nsDirs, err := os.ReadDir(m.state)
	if err != nil {
		return err
	}
	for _, nsd := range nsDirs {
		if !nsd.IsDir() {
			continue
		}
		ns := nsd.Name()
		// skip hidden directories
		if len(ns) > 0 && ns[0] == '.' {
			continue
		}
		log.G(ctx).WithField("namespace", ns).Debug("loading tasks in namespace")
		if err := m.loadTasks(namespaces.WithNamespace(ctx, ns)); err != nil {
			log.G(ctx).WithField("namespace", ns).WithError(err).Error("loading tasks in namespace")
			continue
		}
		if err := m.cleanupWorkDirs(namespaces.WithNamespace(ctx, ns)); err != nil {
			log.G(ctx).WithField("namespace", ns).WithError(err).Error("cleanup working directory in namespace")
			continue
		}
	}
	return nil
}

func (m *TaskManager) loadTasks(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("namespace", ns))

	bundles, err := os.ReadDir(filepath.Join(m.state, ns))
	if err != nil {
		return err
	}
	for _, sd := range bundles {
		if !sd.IsDir() {
			continue
		}
		id := sd.Name()
		// skip hidden directories
		if len(id) > 0 && id[0] == '.' {
			continue
		}
		bundle, err := LoadBundle(ctx, m.state, id)
		if err != nil {
			// fine to return error here, it is a programmer error if the context
			// does not have a namespace
			return err
		}
		// fast path
		f, err := os.Open(bundle.Path)
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}

		bf, err := f.Readdirnames(-1)
		f.Close()
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}
		if len(bf) == 0 {
			bundle.Delete()
			continue
		}

		// Read sandbox ID this task belongs to.
		// it will return ErrorNotExit if it is not a sandboxed task
		sbID, err := os.ReadFile(filepath.Join(bundle.Path, "sandbox"))
		if err == nil {
			loadErr := m.sandboxedTaskManager.Load(ctx, string(sbID), bundle)
			if loadErr != nil && loadErr != ErrCanNotHandle {
				log.G(ctx).WithError(loadErr).Errorf("failed to load %s", bundle.Path)
				bundle.Delete()
			}
			// if loadErr == ErrCanNotHandle, should fallback to the shim load
			if loadErr == nil {
				continue
			}
		}
		if !errors.Is(err, os.ErrNotExist) {
			bundle.Delete()
			continue
		}

		if err := m.shimManager.Load(ctx, bundle); err != nil {
			log.G(ctx).WithError(err).Errorf("failed to load shim %s", bundle.Path)
			bundle.Delete()
			continue
		}
	}
	return nil
}

func (m *TaskManager) cleanupWorkDirs(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	f, err := os.Open(filepath.Join(m.root, ns))
	if err != nil {
		return err
	}
	defer f.Close()

	dirs, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}

	for _, dir := range dirs {
		// if the task was not loaded, cleanup and empty working directory
		// this can happen on a reboot where /run for the bundle state is cleaned up
		// but that persistent working dir is left
		if _, err := m.Get(ctx, dir); err != nil {
			path := filepath.Join(m.root, ns, dir)
			if err := os.RemoveAll(path); err != nil {
				log.G(ctx).WithError(err).Errorf("cleanup working dir %s", path)
			}
		}
	}
	return nil
}
