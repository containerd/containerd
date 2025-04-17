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

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containerd/log"
	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml/v2"
)

type runtimeManager struct {
	sync.RWMutex

	configPath string
	runtimes   map[string]*Runtime
	watcher    *fsnotify.Watcher
}

func newRuntimeManager(runtimeConfigPath string) (*runtimeManager, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	configPathParent := filepath.Dir(runtimeConfigPath)
	if err := os.MkdirAll(configPathParent, 0755); err != nil {
		return nil, fmt.Errorf("failed to create the parent of the runtime config path=%s: %w", configPathParent, err)
	}
	if err := os.MkdirAll(runtimeConfigPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create runtime config dir=%s: %w", runtimeConfigPath, err)
	}

	if err := watcher.Add(runtimeConfigPath); err != nil {
		return nil, fmt.Errorf("failed to add fsnotify watcher for %s: %w", runtimeConfigPath, err)
	}

	rm := &runtimeManager{
		configPath: runtimeConfigPath,
		runtimes:   make(map[string]*Runtime),
		watcher:    watcher,
	}

	entries, err := os.ReadDir(runtimeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime config path %s: %w", runtimeConfigPath, err)
	}
	for _, entry := range entries {
		// don't call rm.load` here to avoid unnecessary locking
		// as we haven't started receiving events yet.
		runtimeClass, runtime, err := loadRuntimeConfig(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to load runtime config from %s: %w", entry.Name(), err)
		}
		if runtimeClass == "" {
			continue
		}
		rm.runtimes[runtimeClass] = runtime
	}

	return rm, nil
}

// start starts watching for changes.
func (rm *runtimeManager) start() error {
	go rm.syncLoop()
	return nil
}

// syncLoop watches for changes in `configPath` and reloads the changes runtimes.
func (rm *runtimeManager) syncLoop() error {
	for {
		select {
		case event, ok := <-rm.watcher.Events:
			if !ok {
				log.L.Debugf("runtime config watcher channel is closed")
				return nil
			}
			// Only reload config when receiving write/rename/remove
			// events
			if event.Has(fsnotify.Chmod) || event.Has(fsnotify.Create) {
				continue
			}
			log.L.Debugf("receiving change event from runtime config path: %s", event)

			// If `configPath` is removed, stop watching.
			if event.Name == rm.configPath && (event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove)) {
				return fmt.Errorf("runtime config path is removed, stop watching")
			}

			rm.load(event.Name)
		case err := <-rm.watcher.Errors:
			if err != nil {
				log.L.WithError(err).Error("failed to continue sync runtime config change")
				return err
			}
		}
	}
}

// close closes the runtime configPath file watcher.
func (rm *runtimeManager) close() error {
	return rm.watcher.Close()
}

// load loads a runtime config from given `filename`.
func (rm *runtimeManager) load(filename string) error {
	runtimeClass, runtime, err := loadRuntimeConfig(filename)
	if err != nil {
		return fmt.Errorf("failed to load runtime config from %s: %w", filename, err)
	}
	if runtimeClass == "" {
		return nil
	}

	rm.Lock()
	defer rm.Unlock()
	rm.runtimes[runtimeClass] = runtime
	return nil
}

// getRuntime returns the runtime associated with `runtimeClass`.
func (rm *runtimeManager) getRuntime(runtimeClass string) (Runtime, bool) {
	if runtimeClass == "" {
		return Runtime{}, false
	}

	rm.RLock()
	defer rm.RUnlock()
	rt, ok := rm.runtimes[runtimeClass]
	return *rt, ok
}

// loadRuntimeConfig loads the runtime config from the given filename.
// It returns the runtime class name and the runtime config.
func loadRuntimeConfig(filename string) (string, *Runtime, error) {
	// Directories and non-TOML files are ignored.
	base := filepath.Base(filename)
	if !strings.HasSuffix(base, ".toml") || len(base) <= 5 {
		return "", nil, nil
	}
	info, err := os.Stat(filename)
	if err != nil {
		return "", nil, fmt.Errorf("failed to stat runtime config file %s: %w", filename, err)
	}
	if info.IsDir() {
		return "", nil, nil
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read TOML file: %w", err)
	}

	runtime := new(Runtime)
	if err := toml.Unmarshal(data, runtime); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal TOML: %w", err)
	}
	if err := validateRuntimeClass(runtime); err != nil {
		return "", nil, fmt.Errorf("invalid runtime config %s: %w", filename, err)
	}

	return base[:len(base)-5], runtime, nil
}
