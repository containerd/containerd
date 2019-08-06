/*
Copyright 2018 The containerd Authors.

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
	"context"
	"flag"
	"path/filepath"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/containers/v1"
	"github.com/containerd/containerd/api/services/diff/v1"
	"github.com/containerd/containerd/api/services/images/v1"
	"github.com/containerd/containerd/api/services/namespaces/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
	"github.com/containerd/containerd/snapshots"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/klog"

	criconfig "github.com/containerd/cri/pkg/config"
	"github.com/containerd/cri/pkg/constants"
	"github.com/containerd/cri/pkg/server"
)

// TODO(random-liu): Use github.com/pkg/errors for our errors.
// Register CRI service plugin
func init() {
	config := criconfig.DefaultConfig()
	plugin.Register(&plugin.Registration{
		Type:   plugin.GRPCPlugin,
		ID:     "cri",
		Config: &config,
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		InitFn: initCRIService,
	})
}

func initCRIService(ic *plugin.InitContext) (interface{}, error) {
	ic.Meta.Platforms = []imagespec.Platform{platforms.DefaultSpec()}
	ic.Meta.Exports = map[string]string{"CRIVersion": constants.CRIVersion}
	ctx := ic.Context
	pluginConfig := ic.Config.(*criconfig.PluginConfig)
	c := criconfig.Config{
		PluginConfig:       *pluginConfig,
		ContainerdRootDir:  filepath.Dir(ic.Root),
		ContainerdEndpoint: ic.Address,
		RootDir:            ic.Root,
		StateDir:           ic.State,
	}
	log.G(ctx).Infof("Start cri plugin with config %+v", c)

	if err := validateConfig(ctx, &c); err != nil {
		return nil, errors.Wrap(err, "invalid config")
	}

	if err := setGLogLevel(); err != nil {
		return nil, errors.Wrap(err, "failed to set glog level")
	}

	servicesOpts, err := getServicesOpts(ic)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get services")
	}

	log.G(ctx).Info("Connect containerd service")
	client, err := containerd.New(
		"",
		containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
		containerd.WithServices(servicesOpts...),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create containerd client")
	}

	s, err := server.NewCRIService(c, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create CRI service")
	}

	go func() {
		if err := s.Run(); err != nil {
			log.G(ctx).WithError(err).Fatal("Failed to run CRI service")
		}
		// TODO(random-liu): Whether and how we can stop containerd.
	}()
	return s, nil
}

// validateConfig validates the given configuration.
func validateConfig(ctx context.Context, c *criconfig.Config) error {
	if c.ContainerdConfig.Runtimes == nil {
		c.ContainerdConfig.Runtimes = make(map[string]criconfig.Runtime)
	}

	// Validation for deprecated untrusted_workload_runtime.
	if c.ContainerdConfig.UntrustedWorkloadRuntime.Type != "" {
		log.G(ctx).Warning("`untrusted_workload_runtime` is deprecated, please use `untrusted` runtime in `runtimes` instead")
		if _, ok := c.ContainerdConfig.Runtimes[criconfig.RuntimeUntrusted]; ok {
			return errors.Errorf("conflicting definitions: configuration includes both `untrusted_workload_runtime` and `runtimes[%q]`", criconfig.RuntimeUntrusted)
		}
		c.ContainerdConfig.Runtimes[criconfig.RuntimeUntrusted] = c.ContainerdConfig.UntrustedWorkloadRuntime
	}

	// Validation for deprecated default_runtime field.
	if c.ContainerdConfig.DefaultRuntime.Type != "" {
		log.G(ctx).Warning("`default_runtime` is deprecated, please use `default_runtime_name` to reference the default configuration you have defined in `runtimes`")
		c.ContainerdConfig.DefaultRuntimeName = criconfig.RuntimeDefault
		c.ContainerdConfig.Runtimes[criconfig.RuntimeDefault] = c.ContainerdConfig.DefaultRuntime
	}

	// Validation for default_runtime_name
	if c.ContainerdConfig.DefaultRuntimeName == "" {
		return errors.New("`default_runtime_name` is empty")
	}
	if _, ok := c.ContainerdConfig.Runtimes[c.ContainerdConfig.DefaultRuntimeName]; !ok {
		return errors.New("no corresponding runtime configured in `runtimes` for `default_runtime_name`")
	}

	// Validation for deprecated runtime options.
	if c.SystemdCgroup {
		if c.ContainerdConfig.Runtimes[c.ContainerdConfig.DefaultRuntimeName].Type != plugin.RuntimeLinuxV1 {
			return errors.Errorf("`systemd_cgroup` only works for runtime %s", plugin.RuntimeLinuxV1)
		}
		log.G(ctx).Warning("`systemd_cgroup` is deprecated, please use runtime `options` instead")
	}
	for _, r := range c.ContainerdConfig.Runtimes {
		if r.Engine != "" {
			if r.Type != plugin.RuntimeLinuxV1 {
				return errors.Errorf("`runtime_engine` only works for runtime %s", plugin.RuntimeLinuxV1)
			}
			log.G(ctx).Warning("`runtime_engine` is deprecated, please use runtime `options` instead")
		}
		if r.Root != "" {
			if r.Type != plugin.RuntimeLinuxV1 {
				return errors.Errorf("`runtime_root` only works for runtime %s", plugin.RuntimeLinuxV1)
			}
			log.G(ctx).Warning("`runtime_root` is deprecated, please use runtime `options` instead")
		}
	}

	// Validation for stream_idle_timeout
	if c.StreamIdleTimeout != "" {
		if _, err := time.ParseDuration(c.StreamIdleTimeout); err != nil {
			return errors.Wrap(err, "invalid stream idle timeout")
		}
	}
	return nil
}

// getServicesOpts get service options from plugin context.
func getServicesOpts(ic *plugin.InitContext) ([]containerd.ServicesOpt, error) {
	plugins, err := ic.GetByType(plugin.ServicePlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get service plugin")
	}

	opts := []containerd.ServicesOpt{
		containerd.WithEventService(ic.Events),
	}
	for s, fn := range map[string]func(interface{}) containerd.ServicesOpt{
		services.ContentService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContentStore(s.(content.Store))
		},
		services.ImagesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithImageService(s.(images.ImagesClient))
		},
		services.SnapshotsService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithSnapshotters(s.(map[string]snapshots.Snapshotter))
		},
		services.ContainersService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContainerService(s.(containers.ContainersClient))
		},
		services.TasksService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithTaskService(s.(tasks.TasksClient))
		},
		services.DiffService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithDiffService(s.(diff.DiffClient))
		},
		services.NamespacesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithNamespaceService(s.(namespaces.NamespacesClient))
		},
		services.LeasesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithLeasesService(s.(leases.Manager))
		},
	} {
		p := plugins[s]
		if p == nil {
			return nil, errors.Errorf("service %q not found", s)
		}
		i, err := p.Instance()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get instance of service %q", s)
		}
		if i == nil {
			return nil, errors.Errorf("instance of service %q not found", s)
		}
		opts = append(opts, fn(i))
	}
	return opts, nil
}

// Set glog level.
func setGLogLevel() error {
	l := logrus.GetLevel()
	fs := flag.NewFlagSet("klog", flag.PanicOnError)
	klog.InitFlags(fs)
	if err := fs.Set("logtostderr", "true"); err != nil {
		return err
	}
	switch l {
	case log.TraceLevel:
		return fs.Set("v", "5")
	case logrus.DebugLevel:
		return fs.Set("v", "4")
	case logrus.InfoLevel:
		return fs.Set("v", "2")
	// glog doesn't support following filters. Defaults to v=0.
	case logrus.WarnLevel:
	case logrus.ErrorLevel:
	case logrus.FatalLevel:
	case logrus.PanicLevel:
	}
	return nil
}
