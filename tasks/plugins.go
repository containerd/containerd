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

package tasks

import "github.com/containerd/containerd/plugin"

// TaskPlugin is the configuration of a proxy plugin.
type TaskPlugin struct {

	// Name is the name of the plugin.
	Name string

	// Type is the type of the plugin.
	Type plugin.Type

	// Address is the address to the unix socket of the plugin.
	Address string
}

// TaskPluginSet defines a plugin collection.
type TaskPluginSet interface {

	// GetAll returns plugins in the set
	GetAll() []*plugin.Plugin

	// GetByType returns all plugins with the specific type.
	GetByType(plugin.Type) (map[string]*plugin.Plugin, error)
}
