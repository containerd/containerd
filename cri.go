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
	"github.com/containerd/containerd/plugin"
	"github.com/golang/glog"

	"github.com/containerd/cri-containerd/cmd/cri-containerd/options"
	"github.com/containerd/cri-containerd/pkg/server"
)

// Register CRI service plugin
func init() {
	plugin.Register(&plugin.Registration{
		// In fact, cri is not strictly a GRPC plugin now.
		Type: plugin.GRPCPlugin,
		ID:   "cri",
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

func initCRIService(_ *plugin.InitContext) (interface{}, error) {
	// TODO(random-liu): Support Config through Registration.Config.
	// TODO(random-liu): Validate the configuration.
	// TODO(random-liu): Leverage other fields in InitContext, such as Root.
	// TODO(random-liu): Register GRPC service onto containerd GRPC server.
	// TODO(random-liu): Separate cri plugin config from cri-containerd server config,
	// because many options only make sense to cri-containerd server.
	// TODO(random-liu): Change all glog to logrus.
	// TODO(random-liu): Handle graceful stop.
	c := options.DefaultConfig()
	glog.V(0).Infof("Start cri plugin with config %+v", c)
	// Use a goroutine to start cri service. The reason is that currently
	// cri service requires containerd to be running.
	// TODO(random-liu): Resolve the circular dependency.
	go func() {
		s, err := server.NewCRIContainerdService(c)
		if err != nil {
			glog.Exitf("Failed to create CRI service: %v", err)
		}
		if err := s.Run(); err != nil {
			glog.Exitf("Failed to run CRI grpc server: %v", err)
		}
	}()
	return nil, nil
}
