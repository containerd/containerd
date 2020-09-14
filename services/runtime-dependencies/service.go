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

package runtimedependencies

import (
	"context"
	"fmt"
	"sync"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/images/v1"
	introspectionapi "github.com/containerd/containerd/api/services/introspection/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.InternalPlugin,
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		ID:     services.RuntimeDependenciesService,
		Config: &PluginConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			if ic.Config == nil {
				logrus.Infof("\"io.containerd.internal.v1.%s\" - no containerd runtime dependencies configured (config nil).", services.RuntimeDependenciesService)
				return nil, nil
			}

			// get own plugin config
			pluginConfig := ic.Config.(*PluginConfig)
			if len(pluginConfig.RuntimeDependencies) == 0 {
				logrus.Infof("\"io.containerd.internal.v1.%s\" - no containerd runtime dependencies configured.", services.RuntimeDependenciesService)
				return nil, nil
			}

			// get required services
			plugins, err := ic.GetByType(plugin.ServicePlugin)
			if err != nil {
				return nil, err
			}

			opts, err := getServicesOpts(plugins)
			if err != nil {
				return nil, err
			}

			// create client with required services
			client, err := containerd.New("", containerd.WithServices(opts...))
			if err != nil {
				return nil, err
			}
			rd := &runtimeDependencies{
				client: client,
			}

			// fetch runtime dependencies
			go rd.run(pluginConfig)
			return rd, nil
		},
	})
}

// getServicesOpts get service options from plugin context.
func getServicesOpts(plugins map[string]*plugin.Plugin) ([]containerd.ServicesOpt, error) {
	opts := []containerd.ServicesOpt{}
	for s, fn := range map[string]func(interface{}) containerd.ServicesOpt{
		// required for client.Pull
		services.ContentService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContentStore(s.(content.Store))
		},
		// required for client.Pull
		services.LeasesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithLeasesService(s.(leases.Manager))
		},
		// required for client.Pull
		services.ImagesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithImageService(s.(images.ImagesClient))
		},
		// required for client.Install
		services.IntrospectionService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithIntrospectionService(s.(introspectionapi.IntrospectionClient))
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

type runtimeDependencies struct {
	client *containerd.Client
}

func (rd *runtimeDependencies) run(config *PluginConfig) {
	logrus.Info("fetching runtime dependencies.")

	var (
		err     error
		channel = make(chan error)
		wg      sync.WaitGroup
	)
	wg.Add(len(config.RuntimeDependencies))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, dependency := range config.RuntimeDependencies {
		if len(dependency.ImageRef) == 0 {
			wg.Done()
			continue
		}
		go func(dep RuntimeDependency) {
			defer wg.Done()
			log.G(ctx).Infof("Loading runtime dependency: %s", dep.ImageRef)
			ctx = namespaces.WithNamespace(ctx, namespaces.Default)
			// fetch image via content store
			image, err := rd.client.Pull(ctx, dep.ImageRef)
			if err != nil {
				channel <- fmt.Errorf("failed to pull runtime dependency with image ref: %s: %v", dep.ImageRef, err)
				return
			}
			log.G(ctx).Infof("Successfully pulled runtime dependency: %s", image.Name())

			var opts []containerd.InstallOpts
			// use install plugin to install the binary to a custom path
			if len(dep.Path) > 0 {
				opts = append(opts, containerd.WithInstallPath(dep.Path))
			}
			if dep.Replace {
				opts = append(opts, containerd.WithInstallReplace)
			}
			if dep.Libs {
				opts = append(opts, containerd.WithInstallLibs)
			}

			if err := rd.client.Install(ctx, image, opts...); err != nil {
				channel <- fmt.Errorf("failed to install runtime dependency with image ref: %s: %v", dep.ImageRef, err)
				return
			}
			log.G(ctx).Infof("Successfully installed runtime dependency: %s", image.Name())
		}(dependency)
	}
	// close channel when wait group has 0 counter
	go func() {
		wg.Wait()
		close(channel)
	}()

	var numFailedDependencies int
	for channelResult := range channel {
		numFailedDependencies++
		err = multierror.Append(err, channelResult)
	}

	if err != nil {
		log.G(ctx).Errorf("Failed to install %d runtime dependencies: %v", numFailedDependencies, err)
		return
	}
	log.G(ctx).Info("Successfully installed all runtime dependencies.")
}
