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
	"os"
	"path/filepath"

	containerd "github.com/containerd/containerd/v2/client"
	"k8s.io/kubelet/pkg/cri/streaming"
)

// DefaultConfig returns default configurations of cri plugin.
func DefaultConfig() PluginConfig {
	return PluginConfig{
		CniConfig: CniConfig{
			NetworkPluginBinDir:        filepath.Join(os.Getenv("ProgramFiles"), "containerd", "cni", "bin"),
			NetworkPluginConfDir:       filepath.Join(os.Getenv("ProgramFiles"), "containerd", "cni", "conf"),
			NetworkPluginMaxConfNum:    1,
			NetworkPluginSetupSerially: false,
			NetworkPluginConfTemplate:  "",
		},
		ContainerdConfig: ContainerdConfig{
			Snapshotter:        containerd.DefaultSnapshotter,
			DefaultRuntimeName: "runhcs-wcow-process",
			Runtimes: map[string]Runtime{
				"runhcs-wcow-process": {
					Type:                 "io.containerd.runhcs.v1",
					ContainerAnnotations: []string{"io.microsoft.container.*"},
				},
				"runhcs-wcow-hypervisor": {
					Type:                 "io.containerd.runhcs.v1",
					PodAnnotations:       []string{"io.microsoft.virtualmachine.*"},
					ContainerAnnotations: []string{"io.microsoft.container.*"},
					// Full set of Windows shim options:
					// https://pkg.go.dev/github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options#Options
					Options: map[string]interface{}{
						// SandboxIsolation specifies the isolation level of the sandbox.
						// PROCESS (0) and HYPERVISOR (1) are the valid options.
						"SandboxIsolation": 1,
						// ScaleCpuLimitsToSandbox indicates that the containers CPU
						// maximum value (specifies the portion of processor cycles that
						// a container can use as a percentage times 100) should be adjusted
						// to account for the difference in the number of cores between the
						// host and UVM.
						//
						// This should only be turned on if SandboxIsolation is 1.
						"ScaleCpuLimitsToSandbox": true,
					},
				},
			},
		},
		DisableTCPService:   true,
		StreamServerAddress: "127.0.0.1",
		StreamServerPort:    "0",
		StreamIdleTimeout:   streaming.DefaultConfig.StreamIdleTimeout.String(), // 4 hour
		EnableTLSStreaming:  false,
		X509KeyPairStreaming: X509KeyPairStreaming{
			TLSKeyFile:  "",
			TLSCertFile: "",
		},
		SandboxImage:              "registry.k8s.io/pause:3.9",
		StatsCollectPeriod:        10,
		MaxContainerLogLineSize:   16 * 1024,
		MaxConcurrentDownloads:    3,
		IgnoreImageDefinedVolumes: false,
		// TODO(windows): Add platform specific config, so that most common defaults can be shared.

		ImageDecryption: ImageDecryption{
			KeyModel: KeyModelNode,
		},
		ImagePullProgressTimeout: defaultImagePullProgressTimeoutDuration.String(),
		DrainExecSyncIOTimeout:   "0s",
	}
}
