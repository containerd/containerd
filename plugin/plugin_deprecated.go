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

package plugin

import (
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/dynamic"
)

var (
	// ErrNoType is returned when no type is specified
	ErrNoType = plugin.ErrNoType
	// ErrNoPluginID is returned when no id is specified
	ErrNoPluginID = plugin.ErrNoPluginID
	// ErrIDRegistered is returned when a duplicate id is already registered
	ErrIDRegistered = plugin.ErrIDRegistered
	// ErrSkipPlugin is used when a plugin is not initialized and should not be loaded,
	// this allows the plugin loader differentiate between a plugin which is configured
	// not to load and one that fails to load.
	ErrSkipPlugin = plugin.ErrSkipPlugin

	// ErrInvalidRequires will be thrown if the requirements for a plugin are
	// defined in an invalid manner.
	ErrInvalidRequires = plugin.ErrInvalidRequires
)

// IsSkipPlugin returns true if the error is skipping the plugin
func IsSkipPlugin(err error) bool {
	return plugin.IsSkipPlugin(err)
}

// Type is the type of the plugin
type Type = plugin.Type

// Meta contains information gathered from the registration and initialization
// process.
type Meta = plugin.Meta

// Load loads all plugins at the provided path into containerd.
//
// Load is currently only implemented on non-static, non-gccgo builds for amd64
// and arm64, and plugins must be built with the exact same version of Go as
// containerd itself.
func Load(path string) (count int, err error) {
	return dynamic.Load(path)
}
