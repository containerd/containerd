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

package cri

import (
	"flag"
	"path/filepath"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/containers/v1"
	"github.com/containerd/containerd/api/services/diff/v1"
	"github.com/containerd/containerd/api/services/images/v1"
	introspectionapi "github.com/containerd/containerd/api/services/introspection/v1"
	"github.com/containerd/containerd/api/services/namespaces/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
	"github.com/containerd/containerd/snapshots"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/klog/v2"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/pkg/cri/server"
	cristore "github.com/containerd/containerd/pkg/cri/store/service"
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
			plugin.EventPlugin,
			plugin.CRIPlugin,
			plugin.CRIServicePlugin,
		},
		InitFn: initCRIService,
	})
}

func initCRIService(ic *plugin.InitContext) (interface{}, error) {
	ic.Meta.Platforms = []imagespec.Platform{platforms.DefaultSpec()}
	ic.Meta.Exports = map[string]string{"CRIVersion": constants.CRIVersion, "CRIVersionAlpha": constants.CRIVersionAlpha}
	ctx := ic.Context
	pluginConfig := ic.Config.(*criconfig.PluginConfig)
	if err := criconfig.ValidatePluginConfig(ctx, pluginConfig); err != nil {
		return nil, errors.Wrap(err, "invalid plugin config")
	}

	c := criconfig.Config{
		PluginConfig:       *pluginConfig,
		ContainerdRootDir:  filepath.Dir(ic.Root),
		ContainerdEndpoint: ic.Address,
		RootDir:            ic.Root,
		StateDir:           ic.State,
	}
	log.G(ctx).Infof("Start cri plugin with config %+v", c)

	if err := setGLogLevel(); err != nil {
		return nil, errors.Wrap(err, "failed to set glog level")
	}

	servicesOpts, err := getServicesOpts(ic)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get services")
	}
	criStore, err := getCRIStore(ic)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get CRI store services")
	}
	criPlugins, err := getCRIPlugin(ic)
	if err != nil && !errors.Is(err, errdefs.ErrNotFound) {
		return nil, errors.Wrap(err, "failed to get CRI plugin")
	}

	log.G(ctx).Info("Connect containerd service")
	client, err := containerd.New(
		"",
		containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
		containerd.WithDefaultPlatform(platforms.Default()),
		containerd.WithServices(servicesOpts...),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create containerd client")
	}

	manager, err := server.NewCRIManager(c, client, criStore, criPlugins)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create CRI manager")
	}

	go func() {
		if err := manager.Run(); err != nil {
			log.G(ctx).WithError(err).Fatal("Failed to run CRI manager")
		}
		// TODO(random-liu): Whether and how we can stop containerd.
	}()
	return manager, nil
}

// getServicesOpts get service options from plugin context.
func getServicesOpts(ic *plugin.InitContext) ([]containerd.ServicesOpt, error) {
	plugins, err := ic.GetByType(plugin.ServicePlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get service plugin")
	}

	ep, err := ic.Get(plugin.EventPlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get event plugin")
	}

	opts := []containerd.ServicesOpt{
		containerd.WithEventService(ep.(containerd.EventService)),
	}
	for s, fn := range map[string]func(interface{}) containerd.ServicesOpt{
		services.ContentService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContentStore(s.(content.Store))
		},
		services.ImagesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithImageClient(s.(images.ImagesClient))
		},
		services.SnapshotsService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithSnapshotters(s.(map[string]snapshots.Snapshotter))
		},
		services.ContainersService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContainerClient(s.(containers.ContainersClient))
		},
		services.TasksService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithTaskClient(s.(tasks.TasksClient))
		},
		services.DiffService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithDiffClient(s.(diff.DiffClient))
		},
		services.NamespacesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithNamespaceClient(s.(namespaces.NamespacesClient))
		},
		services.LeasesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithLeasesService(s.(leases.Manager))
		},
		services.IntrospectionService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithIntrospectionClient(s.(introspectionapi.IntrospectionClient))
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

func getCRIStore(ic *plugin.InitContext) (*cristore.Store, error) {
	plugins, err := ic.GetByType(plugin.CRIServicePlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cri store")
	}
	p := plugins[cristore.CRIStoreService]
	if p == nil {
		return nil, errors.Errorf("cri service store not found")
	}
	i, err := p.Instance()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get instance of cri service store")
	}
	return i.(*cristore.Store), nil
}

// getCRIPlugin get cri services from plugin context
func getCRIPlugin(ic *plugin.InitContext) (map[string]server.CRIPlugin, error) {
	criPlugins := map[string]server.CRIPlugin{}
	plugins, err := ic.GetByType(plugin.CRIPlugin)
	if err != nil {
		return criPlugins, errors.Wrap(err, "failed to get cri plugin")
	}
	for k, v := range plugins {
		i, err := v.Instance()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get instance of service %q", k)
		}
		// plugin.Registration.ID as key
		criPlugins[k] = i.(server.CRIPlugin)
	}
	return criPlugins, nil
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
	case logrus.TraceLevel:
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
