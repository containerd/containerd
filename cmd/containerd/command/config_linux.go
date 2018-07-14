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
	"github.com/containerd/containerd/rootless"
	"github.com/containerd/containerd/services/server"
)

func defaultConfig() *server.Config {
	c := &server.Config{
		Root:  defaults.DefaultRootDir,
		State: defaults.DefaultStateDir,
		GRPC: server.GRPCConfig{
			Address:        defaults.DefaultAddress,
			MaxRecvMsgSize: defaults.DefaultMaxRecvMsgSize,
			MaxSendMsgSize: defaults.DefaultMaxSendMsgSize,
		},
	}
	if rootless.RunningWithNonRootUsername {
		c.Root = defaults.UserRootDir
		c.State = defaults.UserStateDir
		c.GRPC.Address = defaults.UserAddress
	}
	return c
}
