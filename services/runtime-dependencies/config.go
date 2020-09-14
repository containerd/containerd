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

package runtimedependencies

import "github.com/containerd/containerd"

// PluginConfig contains toml config related to the runtime dependencies plugin
type PluginConfig struct {
	// RuntimeDependencies are package dependencies that containerd should install at startup
	RuntimeDependencies map[string]RuntimeDependency `toml:"dependencies"`
}

// RuntimeDependency is a runtime dependency in form of an OCI image that shall be loaded on startup of containerd
type RuntimeDependency struct {
	// ImageRef is an OCI image reference that can be loaded via `ctr pull` / `client.Pull`
	ImageRef string `toml:"image_ref"`
	// InstallConfig is the install config.
	containerd.InstallConfig
}
