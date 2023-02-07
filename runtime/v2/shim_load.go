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
	"os"
	"path/filepath"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/cleanup"
)

func (m *ShimManager) loadExistingTasks(ctx context.Context) error {
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
		if err := m.loadShims(namespaces.WithNamespace(ctx, ns)); err != nil {
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

func (m *ShimManager) loadShims(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("namespace", ns))

	shimDirs, err := os.ReadDir(filepath.Join(m.state, ns))
	if err != nil {
		return err
	}
	for _, sd := range shimDirs {
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
		bf, err := os.ReadDir(bundle.Path)
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Errorf("fast path read bundle path for %s", bundle.Path)
			continue
		}
		if len(bf) == 0 {
			bundle.Delete()
			continue
		}

		var (
			runtime string
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
				if err := mount.UnmountRecursive(filepath.Join(bundle.Path, "rootfs"), 0); err != nil {
					log.G(ctx).WithError(err).Errorf("failed to unmount of rootfs %s", id)
				}
				bundle.Delete()
				continue
			}
			runtime = container.Runtime.Name
		}

		runtime, err = m.resolveRuntimePath(runtime)
		if err != nil {
			bundle.Delete()
			log.G(ctx).WithError(err).Error("failed to resolve runtime path")
			continue
		}

		binaryCall := shimBinary(bundle,
			shimBinaryConfig{
				runtime:      runtime,
				address:      m.containerdAddress,
				ttrpcAddress: m.containerdTTRPCAddress,
				schedCore:    m.schedCore,
			})
		instance, err := loadShim(ctx, bundle, func() {
			log.G(ctx).WithField("id", id).Info("shim disconnected")

			cleanupAfterDeadShim(cleanup.Background(ctx), id, m.shims, m.events, binaryCall)
			// Remove self from the runtime task list.
			m.shims.Delete(ctx, id)
		})
		if err != nil {
			cleanupAfterDeadShim(ctx, id, m.shims, m.events, binaryCall)
			continue
		}
		shim := newShimTask(instance)

		// There are 3 possibilities for the loaded shim here:
		// 1. It could be a shim that is running a task.
		// 2. It could be a sandbox shim.
		// 3. Or it could be a shim that was created for running a task but
		// something happened (probably a containerd crash) and the task was never
		// created. This shim process should be cleaned up here. Look at
		// containerd/containerd#6860 for further details.

		_, sgetErr := m.sandboxStore.Get(ctx, id)
		pInfo, pidErr := shim.Pids(ctx)
		if sgetErr != nil && errors.Is(sgetErr, errdefs.ErrNotFound) && (len(pInfo) == 0 || errors.Is(pidErr, errdefs.ErrNotFound)) {
			log.G(ctx).WithField("id", id).Info("cleaning leaked shim process")
			// We are unable to get Pids from the shim and it's not a sandbox
			// shim. We should clean it up her.
			// No need to do anything for removeTask since we never added this shim.
			shim.delete(ctx, false, func(ctx context.Context, id string) {})
		} else {
			m.shims.Add(ctx, shim.ShimInstance)
		}
	}
	return nil
}

func (m *ShimManager) cleanupWorkDirs(ctx context.Context) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	dirs, err := os.ReadDir(filepath.Join(m.root, ns))
	if err != nil {
		return err
	}
	for _, d := range dirs {
		// if the task was not loaded, cleanup and empty working directory
		// this can happen on a reboot where /run for the bundle state is cleaned up
		// but that persistent working dir is left
		if _, err := m.shims.Get(ctx, d.Name()); err != nil {
			path := filepath.Join(m.root, ns, d.Name())
			if err := os.RemoveAll(path); err != nil {
				log.G(ctx).WithError(err).Errorf("cleanup working dir %s", path)
			}
		}
	}
	return nil
}
