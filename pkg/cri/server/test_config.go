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

package server

import criconfig "github.com/containerd/containerd/v2/pkg/cri/config"

const (
	testRootDir  = "/test/root"
	testStateDir = "/test/state"
)

var testConfig = criconfig.Config{
	RootDir:  testRootDir,
	StateDir: testStateDir,
	PluginConfig: criconfig.PluginConfig{
		TolerateMissingHugetlbController: true,
		ContainerdConfig: criconfig.ContainerdConfig{
			DefaultRuntimeName: "runc",
			Runtimes: map[string]criconfig.Runtime{
				"runc": {
					Type:        "runc",
					Snapshotter: "overlayfs",
					Sandboxer:   "shim",
				},
			},
		},
	},
}
