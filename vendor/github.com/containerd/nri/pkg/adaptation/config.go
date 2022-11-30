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

package adaptation

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

const (
	// PluginConfigSubdir is the drop-in directory for plugin configuration.
	PluginConfigSubdir = "conf.d"
)

// Config is the runtime configuration for NRI.
type Config struct {
	// DisableConnections disables plugin-initiated connections.
	DisableConnections bool `json:"disableConnections"`

	path   string
	dropIn string
}

// DefaultConfig returns the default NRI configuration for a given path.
// This configuration should be identical to what ReadConfig would return
// for an empty file at the given location. If the given path is empty,
// DefaultConfigPath is used instead.
func DefaultConfig(path string) *Config {
	if path == "" {
		path = DefaultConfigPath
	}
	return &Config{
		path:   path,
		dropIn: filepath.Join(filepath.Dir(path), PluginConfigSubdir),
	}
}

// ReadConfig reads the NRI runtime configuration from a file.
func ReadConfig(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", path, err)
	}

	cfg := &Config{}
	err = yaml.UnmarshalStrict(buf, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file %q: %w", path, err)
	}

	cfg.path = path
	cfg.dropIn = filepath.Join(filepath.Dir(path), PluginConfigSubdir)

	return cfg, nil
}

func (cfg *Config) getPluginConfig(id, base string) (string, error) {
	name := id + "-" + base
	dropIns := []string{
		filepath.Join(cfg.dropIn, name+".conf"),
		filepath.Join(cfg.dropIn, base+".conf"),
	}

	for _, path := range dropIns {
		buf, err := os.ReadFile(path)
		if err == nil {
			return string(buf), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to read configuration for plugin %q: %w", name, err)
		}
	}

	return "", nil
}
