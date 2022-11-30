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

package nri

import (
	nri "github.com/containerd/nri/pkg/adaptation"
)

// Config data for NRI.
type Config struct {
	// Disable this NRI plugin and containerd NRI functionality altogether.
	Disable bool `toml:"disable" json:"disable"`
	// ConfigPath is the path to the NRI configuration file to use.
	ConfigPath string `toml:"config_file" json:"configFile"`
	// SocketPath is the path to the NRI socket to create for NRI plugins to connect to.
	SocketPath string `toml:"socket_path" json:"socketPath"`
	// PluginPath is the path to search for NRI plugins to launch on startup.
	PluginPath string `toml:"plugin_path" json:"pluginPath"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Disable:    true,
		ConfigPath: nri.DefaultConfigPath,
		SocketPath: nri.DefaultSocketPath,
		PluginPath: nri.DefaultPluginPath,
	}
}

// toOptions returns NRI options for this configuration.
func (c *Config) toOptions() []nri.Option {
	opts := []nri.Option{}
	if c.ConfigPath != "" {
		opts = append(opts, nri.WithConfigPath(c.ConfigPath))
	}
	if c.SocketPath != "" {
		opts = append(opts, nri.WithSocketPath(c.SocketPath))
	}
	if c.PluginPath != "" {
		opts = append(opts, nri.WithPluginPath(c.PluginPath))
	}
	return opts
}
