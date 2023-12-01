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
	"fmt"

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/pkg/cri/nri"
	"github.com/containerd/containerd/v2/pkg/cri/server"
	"github.com/containerd/containerd/v2/pkg/cri/server/base"
	"github.com/containerd/containerd/v2/pkg/cri/server/images"
	nriservice "github.com/containerd/containerd/v2/pkg/nri"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
)

// Register CRI service plugin
func init() {

	registry.Register(&plugin.Registration{
		Type: plugins.GRPCPlugin,
		ID:   "cri",
		Requires: []plugin.Type{
			plugins.CRIImagePlugin,
			plugins.InternalPlugin,
			plugins.SandboxControllerPlugin,
			plugins.NRIApiPlugin,
			plugins.EventPlugin,
			plugins.ServicePlugin,
			plugins.LeasePlugin,
			plugins.SandboxStorePlugin,
		},
		InitFn: initCRIService,
	})
}

func initCRIService(ic *plugin.InitContext) (interface{}, error) {
	ctx := ic.Context

	// Get base CRI dependencies.
	criBasePlugin, err := ic.GetByID(plugins.InternalPlugin, "cri")
	if err != nil {
		return nil, fmt.Errorf("unable to load CRI service base dependencies: %w", err)
	}
	criBase := criBasePlugin.(*base.CRIBase)

	// Get image service.
	criImagePlugin, err := ic.GetByID(plugins.CRIImagePlugin, "cri-image-service")
	if err != nil {
		return nil, fmt.Errorf("unable to load CRI image service plugin dependency: %w", err)
	}
	imageService := criImagePlugin.(*images.CRIImageService)

	log.G(ctx).Info("Connect containerd service")
	client, err := containerd.New(
		"",
		containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
		containerd.WithDefaultPlatform(platforms.Default()),
		containerd.WithInMemoryServices(ic),
		containerd.WithInMemorySandboxControllers(ic),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd client: %w", err)
	}

	s, err := server.NewCRIService(criBase, imageService, client, getNRIAPI(ic))
	if err != nil {
		return nil, fmt.Errorf("failed to create CRI service: %w", err)
	}

	// RegisterReadiness() must be called after NewCRIService(): https://github.com/containerd/containerd/issues/9163
	ready := ic.RegisterReadiness()
	go func() {
		if err := s.Run(ready); err != nil {
			log.G(ctx).WithError(err).Fatal("Failed to run CRI service")
		}
		// TODO(random-liu): Whether and how we can stop containerd.
	}()

	return s, nil
}

// Get the NRI plugin, and set up our NRI API for it.
func getNRIAPI(ic *plugin.InitContext) *nri.API {
	const (
		pluginType = plugins.NRIApiPlugin
		pluginName = "nri"
	)

	ctx := ic.Context

	p, err := ic.GetByID(pluginType, pluginName)
	if err != nil {
		log.G(ctx).Info("NRI service not found, NRI support disabled")
		return nil
	}

	api, ok := p.(nriservice.API)
	if !ok {
		log.G(ctx).Infof("NRI plugin (%s, %q) has incorrect type %T, NRI support disabled",
			pluginType, pluginName, api)
		return nil
	}

	log.G(ctx).Info("using experimental NRI integration - disable nri plugin to prevent this")

	return nri.NewAPI(api)
}
