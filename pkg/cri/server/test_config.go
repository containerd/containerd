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

import criconfig "github.com/containerd/containerd/pkg/cri/config"

const (
	testRootDir  = "/test/root"
	testStateDir = "/test/state"
	// Use an image id as test sandbox image to avoid image name resolve.
	// TODO(random-liu): Change this to image name after we have complete image
	// management unit test framework.
	testSandboxImage = "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113798" // #nosec G101
)

var testConfig = criconfig.Config{
	RootDir:  testRootDir,
	StateDir: testStateDir,
	PluginConfig: criconfig.PluginConfig{
		SandboxImage:                     testSandboxImage,
		TolerateMissingHugetlbController: true,
		ContainerdConfig: criconfig.ContainerdConfig{
			Snapshotter:        "overlayfs",
			DefaultRuntimeName: "runc",
			Runtimes: map[string]criconfig.Runtime{
				"runc": {
					Type:        "runc",
					Snapshotter: "overlayfs",
				},
			},
		},
	},
}
