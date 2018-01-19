/*
Copyright 2018 The Containerd Authors.

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

package cri

import (
	"path/filepath"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/pkg/errors"

	"github.com/containerd/cri-containerd/cmd/cri-containerd/options"
	"github.com/containerd/cri-containerd/pkg/server"
)

// TODO(random-liu): Use github.com/pkg/errors for our errors.
// Register CRI service plugin
func init() {
	// TODO(random-liu): Make `containerd config default` print plugin default config.
	config := options.DefaultConfig().PluginConfig
	plugin.Register(&plugin.Registration{
		// In fact, cri is not strictly a GRPC plugin now.
		Type:   plugin.GRPCPlugin,
		ID:     "cri",
		Config: &config,
		Requires: []plugin.Type{
			plugin.RuntimePlugin,
			plugin.SnapshotPlugin,
			plugin.TaskMonitorPlugin,
			plugin.DiffPlugin,
			plugin.MetadataPlugin,
			plugin.ContentPlugin,
			plugin.GCPlugin,
		},
		InitFn: initCRIService,
	})
}

func initCRIService(ic *plugin.InitContext) (interface{}, error) {
	ctx := ic.Context
	// TODO(random-liu): Handle graceful stop.
	pluginConfig := ic.Config.(*options.PluginConfig)
	c := options.CRIConfig{
		PluginConfig: *pluginConfig,
		// This is a hack. We assume that containerd root directory
		// is one level above plugin directory.
		// TODO(random-liu): Expose containerd config to plugin.
		ContainerdRootDir:  filepath.Dir(ic.Root),
		ContainerdEndpoint: ic.Address,
		RootDir:            ic.Root,
	}
	log.G(ctx).Infof("Start cri plugin with config %+v", c)

	s, err := server.NewCRIContainerdService(c)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create CRI service")
	}

	// Use a goroutine to initialize cri service. The reason is that currently
	// cri service requires containerd to be initialize.
	go func() {
		if err := s.Run(false); err != nil {
			log.G(ctx).WithError(err).Fatal("Failed to run CRI service")
		}
		// TODO(random-liu): Whether and how we can stop containerd.
	}()
	return s, nil
}
