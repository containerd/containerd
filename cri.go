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
	"flag"
	"path/filepath"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	criconfig "github.com/containerd/cri-containerd/pkg/config"
	"github.com/containerd/cri-containerd/pkg/server"
)

// criVersion is the CRI version supported by the CRI plugin.
const criVersion = "v1alpha2"

// TODO(random-liu): Use github.com/pkg/errors for our errors.
// Register CRI service plugin
func init() {
	config := criconfig.DefaultConfig()
	plugin.Register(&plugin.Registration{
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
	ic.Meta.Platforms = []imagespec.Platform{platforms.DefaultSpec()}
	ic.Meta.Exports = map[string]string{"CRIVersion": criVersion}
	ctx := ic.Context
	pluginConfig := ic.Config.(*criconfig.PluginConfig)
	c := criconfig.Config{
		PluginConfig: *pluginConfig,
		// This is a hack. We assume that containerd root directory
		// is one level above plugin directory.
		// TODO(random-liu): Expose containerd config to plugin.
		ContainerdRootDir:  filepath.Dir(ic.Root),
		ContainerdEndpoint: ic.Address,
		RootDir:            ic.Root,
	}
	log.G(ctx).Infof("Start cri plugin with config %+v", c)

	if err := setGLogLevel(); err != nil {
		return nil, errors.Wrap(err, "failed to set glog level")
	}

	s, err := server.NewCRIContainerdService(c)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create CRI service")
	}

	// Use a goroutine to initialize cri service. The reason is that currently
	// cri service requires containerd to be initialize.
	go func() {
		if err := s.Run(); err != nil {
			log.G(ctx).WithError(err).Fatal("Failed to run CRI service")
		}
		// TODO(random-liu): Whether and how we can stop containerd.
	}()
	return s, nil
}

// Set glog level.
func setGLogLevel() error {
	l := logrus.GetLevel()
	if err := flag.Set("logtostderr", "true"); err != nil {
		return err
	}
	switch l {
	case log.TraceLevel:
		return flag.Set("v", "5")
	case logrus.DebugLevel:
		return flag.Set("v", "4")
	case logrus.InfoLevel:
		return flag.Set("v", "2")
	// glog doesn't support following filters. Defaults to v=0.
	case logrus.WarnLevel:
	case logrus.ErrorLevel:
	case logrus.FatalLevel:
	case logrus.PanicLevel:
	}
	return nil
}
