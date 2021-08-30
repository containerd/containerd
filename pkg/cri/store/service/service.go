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

package service

import (
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/images/v1"
	"github.com/containerd/containerd/api/services/namespaces/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"

	"github.com/containerd/containerd/pkg/cri/constants"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	"github.com/containerd/containerd/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/containerd/pkg/registrar"
)

// CRIStoreService is the Plugin ID
const CRIStoreService = "cri-store"

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.CRIServicePlugin,
		ID:   CRIStoreService,
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			servicesOpts, err := getServicesOpts(ic)
			if err != nil {
				return nil, fmt.Errorf("failed to get services: %w", err)
			}

			client, err := containerd.New(
				"",
				containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
				containerd.WithDefaultPlatform(platforms.Default()),
				containerd.WithServices(servicesOpts...),
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create containerd client: %w", err)
			}
			labels := label.NewStore()
			store := &Store{
				SandboxStore:       sandboxstore.NewStore(labels),
				ContainerStore:     containerstore.NewStore(labels),
				ImageStore:         imagestore.NewStore(client),
				SandboxNameIndex:   registrar.NewRegistrar(),
				ContainerNameIndex: registrar.NewRegistrar(),
			}
			return store, nil
		},
	})
}

// Store stores all resources associated with cri
type Store struct {
	// sandboxStore stores all resources associated with sandboxes.
	SandboxStore *sandboxstore.Store
	// SandboxNameIndex stores all sandbox names and make sure each name
	// is unique.
	SandboxNameIndex *registrar.Registrar
	// containerStore stores all resources associated with containers.
	ContainerStore *containerstore.Store
	// ContainerNameIndex stores all container names and make sure each
	// name is unique.
	ContainerNameIndex *registrar.Registrar
	// imageStore stores all resources associated with images.
	ImageStore *imagestore.Store
}

// getServicesOpts get service options from plugin context.
func getServicesOpts(ic *plugin.InitContext) ([]containerd.ServicesOpt, error) {
	plugins, err := ic.GetByType(plugin.ServicePlugin)
	if err != nil {
		return nil, fmt.Errorf("failed to get service plugin: %w", err)
	}

	opts := []containerd.ServicesOpt{
		containerd.WithEventService(ic.Events),
	}
	for s, fn := range map[string]func(interface{}) containerd.ServicesOpt{
		services.ContentService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContentStore(s.(content.Store))
		},
		services.ImagesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithImageClient(s.(images.ImagesClient))
		},
		services.NamespacesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithNamespaceClient(s.(namespaces.NamespacesClient))
		},
	} {
		p := plugins[s]
		if p == nil {
			return nil, fmt.Errorf("service %q not found", s)
		}
		i, err := p.Instance()
		if err != nil {
			return nil, fmt.Errorf("failed to get instance of service %q: %w", s, err)
		}
		if i == nil {
			return nil, fmt.Errorf("instance of service %q not found", s)
		}
		opts = append(opts, fn(i))
	}
	return opts, nil
}
