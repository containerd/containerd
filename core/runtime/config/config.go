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

// Package config loads runtime handler configuration shared by containerd
// clients and plugins.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

const configFile = "runtime.toml"

// Runtime contains the runtime handler fields shared by containerd consumers.
// Consumers may define a superset with additional fields and pass it to Load.
type Runtime struct {
	// Type is the runtime type, such as io.containerd.runc.v2.
	Type string `toml:"runtime_type" json:"runtimeType"`
	// Path optionally overrides the shim binary path.
	Path string `toml:"runtime_path" json:"runtimePath"`
	// Options contains runtime-specific configuration.
	Options map[string]any `toml:"options" json:"options"`
	// Snapshotter optionally overrides the snapshotter for this runtime.
	Snapshotter string `toml:"snapshotter" json:"snapshotter"`
}

// Load reads runtime configurations from <dir>/<handler>/runtime.toml into T.
// Consumers can use Runtime for the common schema or define a superset with
// additional consumer-specific TOML fields. A missing directory is treated as
// an empty configuration.
func Load[T any](dir string) (map[string]T, error) {
	runtimes := make(map[string]T)
	if dir == "" {
		return runtimes, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtimes, nil
		}
		return nil, fmt.Errorf("failed to read runtime config directory %q: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		path := filepath.Join(dir, entry.Name(), configFile)
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to read runtime config for %q from %q: %w", entry.Name(), path, err)
		}

		var runtime T
		if err := toml.Unmarshal(data, &runtime); err != nil {
			return nil, fmt.Errorf("failed to decode runtime config for %q from %q: %w", entry.Name(), path, err)
		}
		runtimes[entry.Name()] = runtime
	}

	return runtimes, nil
}
