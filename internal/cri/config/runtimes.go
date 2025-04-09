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

	if err := rm.start(); err != nil {
		return nil, fmt.Errorf("failed to start runtime manager: %w", err)
	}
	return rm, nil
}

func (rm *runtimeManager) start() error {
	rm.Lock()
	defer rm.Unlock()

	// start watcher first and then load the runtimes, so we won't
	// miss modifications during the initial load.
	go rm.syncLoop()

	entries, err := os.ReadDir(rm.configPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		rm.load(entry.Name())
	}

	return nil
}

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

			// If the confDir is removed, stop watching.
			if event.Name == rm.configPath && (event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove)) {
				return fmt.Errorf("cni conf dir is removed, stop watching")
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

func (rm *runtimeManager) load(filename string) error {
	base := filepath.Base(filename)
	if !strings.HasSuffix(base, ".toml") || len(base) <= 5 {
		return nil
	}

	info, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("failed to stat runtime config file %s: %w", filename, err)
	}
	if info.IsDir() {
		return nil
	}

	// both runtime config load and adding to `runtimes` map should be protected
	// by the mutex.
	rm.Lock()
	defer rm.Unlock()
	rc, err := loadRuntimeConfig(filename)
	if err != nil {
		return fmt.Errorf("failed to load runtime config from %s: %w", filename, err)
	}

	validateRuntimeClass(rc)
	rm.runtimes[base[:len(base)-5]] = rc
	return nil
}

func (rm *runtimeManager) close() error {
	return rm.watcher.Close()
}

func (rm *runtimeManager) getRuntime(runtimeClass string) (Runtime, bool) {
	return Runtime{}, false
}

func loadRuntimeConfig(path string) (*Runtime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read TOML file: %w", err)
	}

	runtime := new(Runtime)
	if err := toml.Unmarshal(data, runtime); err != nil {
		return nil, fmt.Errorf("failed to unmarshal TOML: %w", err)
	}

	return runtime, nil
}
