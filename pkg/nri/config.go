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
	"github.com/containerd/containerd/pkg/tomlext"
	nri "github.com/containerd/nri/pkg/adaptation"
)

// Config data for NRI.
type Config struct {
	// Disable this NRI plugin and containerd NRI functionality altogether.
	Disable bool `toml:"disable" json:"disable"`
	// SocketPath is the path to the NRI socket to create for NRI plugins to connect to.
	SocketPath string `toml:"socket_path" json:"socketPath"`
	// PluginPath is the path to search for NRI plugins to launch on startup.
	PluginPath string `toml:"plugin_path" json:"pluginPath"`
	// PluginConfigPath is the path to search for plugin-specific configuration.
	PluginConfigPath string `toml:"plugin_config_path" json:"pluginConfigPath"`
	// PluginRegistrationTimeout is the timeout for plugin registration.
	PluginRegistrationTimeout tomlext.Duration `toml:"plugin_registration_timeout" json:"pluginRegistrationTimeout"`
	// PluginRequestTimeout is the timeout for a plugin to handle a request.
	PluginRequestTimeout tomlext.Duration `toml:"plugin_request_timeout" json:"pluginRequestTimeout"`
	// DisableConnections disables connections from externally launched plugins.
	DisableConnections bool `toml:"disable_connections" json:"disableConnections"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Disable:          true,
		SocketPath:       nri.DefaultSocketPath,
		PluginPath:       nri.DefaultPluginPath,
		PluginConfigPath: nri.DefaultPluginConfigPath,

		PluginRegistrationTimeout: tomlext.FromStdTime(nri.DefaultPluginRegistrationTimeout),
		PluginRequestTimeout:      tomlext.FromStdTime(nri.DefaultPluginRequestTimeout),
	}
}

// toOptions returns NRI options for this configuration.
func (c *Config) toOptions() []nri.Option {
	opts := []nri.Option{}
	if c.SocketPath != "" {
		opts = append(opts, nri.WithSocketPath(c.SocketPath))
	}
	if c.PluginPath != "" {
		opts = append(opts, nri.WithPluginPath(c.PluginPath))
	}
	if c.PluginConfigPath != "" {
		opts = append(opts, nri.WithPluginConfigPath(c.PluginConfigPath))
	}
	if c.DisableConnections {
		opts = append(opts, nri.WithDisabledExternalConnections())
	}
	return opts
}

// ConfigureTimeouts sets timeout options for NRI.
func (c *Config) ConfigureTimeouts() {
	if c.PluginRegistrationTimeout != 0 {
		nri.SetPluginRegistrationTimeout(tomlext.ToStdTime(c.PluginRegistrationTimeout))
	}
	if c.PluginRequestTimeout != 0 {
		nri.SetPluginRequestTimeout(tomlext.ToStdTime(c.PluginRequestTimeout))
	}
}
