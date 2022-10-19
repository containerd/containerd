//go:build linux
// +build linux

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
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/containerd/containerd/pkg/process"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime/v2/runc/embed"
	"github.com/containerd/containerd/runtime/v2/shim"
	"github.com/containerd/containerd/sandbox/linux"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type SandboxConfig struct {
	PauseLocation    string `toml:"pause_location" json:"pause_location"`
	TaskTTRPCAddress string `toml:"task_ttrpc_address" json:"task_ttrpc_address"`
}

const defaultTaskTTRPCAddress = "unix:///run/containerd/linux-task.sock"

func init() {
	plugin.Register(&plugin.Registration{
		ID:     "linux-sandbox",
		Type:   plugin.SandboxPlugin,
		Config: &SandboxConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = []ocispec.Platform{platforms.DefaultSpec()}
			root := ic.Root
			state := ic.State
			pauseLocation := "/bin/pause"

			config, ok := ic.Config.(*SandboxConfig)
			if !ok {
				return nil, errors.New("invalid btrfs configuration")
			}

			if config.TaskTTRPCAddress == "" {
				config.TaskTTRPCAddress = defaultTaskTTRPCAddress
			}
			if !strings.HasPrefix(config.TaskTTRPCAddress, "unix://") {
				config.TaskTTRPCAddress = fmt.Sprintf("unix://%s", config.TaskTTRPCAddress)
			}
			if config.PauseLocation != "" {
				pauseLocation = config.PauseLocation
			}
			ic.Meta.Exports = map[string]string{"root": root}
			return linux.NewSandboxController(ic.Context, root, state, pauseLocation, config.TaskTTRPCAddress)
		},
	})

	plugin.Register(&plugin.Registration{
		Type:   plugin.TaskPlugin,
		ID:     "linux-task",
		Config: &embed.TaskConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			state := ic.State
			publisher, err := shim.NewPublisher(ic.TTRPCAddress)
			if err != nil {
				return nil, err
			}
			config, ok := ic.Config.(*embed.TaskConfig)
			if !ok {
				return nil, errors.New("invalid btrfs configuration")
			}
			if config.Root == "" {
				config.Root = process.RuncRoot
			}
			if config.TTRPCAddress == "" {
				config.TTRPCAddress = defaultTaskTTRPCAddress
			}

			if !strings.HasPrefix(config.TTRPCAddress, "unix://") {
				config.TTRPCAddress = fmt.Sprintf("unix://%s", config.TTRPCAddress)
			}
			if err := os.MkdirAll(ic.State, 0711); err != nil {
				return nil, err
			}
			return embed.NewTaskService(ic.Context, publisher, state, config)
		},
	})
}
