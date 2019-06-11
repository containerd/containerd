// +build !linux,!windows,!solaris

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

package command

import (
	"github.com/containerd/containerd/defaults"
	srvconfig "github.com/containerd/containerd/services/server/config"
)

func defaultConfig() *srvconfig.Config {
	return &srvconfig.Config{
		Version: 1,
		Root:    defaults.DefaultRootDir,
		State:   defaults.DefaultStateDir,
		GRPC: srvconfig.GRPCConfig{
			Address: defaults.DefaultAddress,
		},
		Debug: srvconfig.Debug{
			Level:   "info",
			Address: defaults.DefaultDebugAddress,
		},
	}
}
