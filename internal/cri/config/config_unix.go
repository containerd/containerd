//go:build !windows

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
	"github.com/containerd/containerd/v2/defaults"
	"github.com/pelletier/go-toml/v2"
)

func defaultNetworkPluginBinDirs() []string {
	return []string{"/opt/cni/bin"}
}

func DefaultImageConfig() ImageConfig {
	return ImageConfig{
		Snapshotter:                defaults.DefaultSnapshotter,
		DisableSnapshotAnnotations: true,
		MaxConcurrentDownloads:     3,
		ImageDecryption: ImageDecryption{
			KeyModel: KeyModelNode,
		},
		PinnedImages: map[string]string{
			"sandbox": DefaultSandboxImage,
		},
		ImagePullProgressTimeout: defaultImagePullProgressTimeoutDuration.String(),
		ImagePullWithSyncFs:      false,
		StatsCollectPeriod:       10,
	}
}

// DefaultRuntimeConfig returns default configurations of cri plugin.
func DefaultRuntimeConfig() RuntimeConfig {
	defaultRuncV2Opts := `
	# NoNewKeyring disables new keyring for the container.
	NoNewKeyring = false

	# ShimCgroup places the shim in a cgroup.
	ShimCgroup = ""

	# IoUid sets the I/O's pipes uid.
	IoUid = 0

	# IoGid sets the I/O's pipes gid.
	IoGid = 0

	# BinaryName is the binary name of the runc binary.
	BinaryName = ""

	# Root is the runc root directory.
	Root = ""

	# SystemdCgroup enables systemd cgroups.
	SystemdCgroup = false

	# CriuImagePath is the criu image path
	CriuImagePath = ""

	# CriuWorkPath is the criu work path.
	CriuWorkPath = ""
`
	var m map[string]interface{}
	toml.Unmarshal([]byte(defaultRuncV2Opts), &m)

	return RuntimeConfig{
		CniConfig: CniConfig{
			NetworkPluginBinDirs:       defaultNetworkPluginBinDirs(),
			NetworkPluginConfDir:       "/etc/cni/net.d",
			NetworkPluginMaxConfNum:    1, // only one CNI plugin config file will be loaded
			NetworkPluginSetupSerially: false,
			NetworkPluginConfTemplate:  "",
			UseInternalLoopback:        false,
		},
		ContainerdConfig: ContainerdConfig{
			DefaultRuntimeName: "runc",
			Runtimes: map[string]Runtime{
				"runc": {
					Type:      "io.containerd.runc.v2",
					Options:   m,
					Sandboxer: string(ModePodSandbox),
				},
			},
		},
		EnableSelinux:                    false,
		SelinuxCategoryRange:             1024,
		MaxContainerLogLineSize:          16 * 1024,
		DisableProcMount:                 false,
		TolerateMissingHugetlbController: true,
		DisableHugetlbController:         true,
		IgnoreImageDefinedVolumes:        false,
		EnableCDI:                        true,
		CDISpecDirs:                      []string{"/etc/cdi", "/var/run/cdi"},
		DrainExecSyncIOTimeout:           "0s",
		EnableUnprivilegedPorts:          true,
		EnableUnprivilegedICMP:           true,
	}
}
