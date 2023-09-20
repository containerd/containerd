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
	"os"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/cri/nri"
	"github.com/containerd/containerd/pkg/cri/sbserver"
	"github.com/containerd/containerd/pkg/cri/sbserver/base"
	"github.com/containerd/containerd/pkg/cri/sbserver/images"
	"github.com/containerd/containerd/pkg/cri/server"
	nriservice "github.com/containerd/containerd/pkg/nri"
	"github.com/containerd/containerd/plugin"
)

// Register CRI service plugin
func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.GRPCPlugin,
		ID:     "cri-runtime-service",
		InitFn: initCRIService,
	})
}

func initCRIService(ic *plugin.InitContext) (interface{}, error) {
	var (
		ctx   = ic.Context
		err   error
		ready = ic.RegisterReadiness()
	)

	// Get base CRI dependencies.
	criBasePlugin, err := ic.GetByID(plugin.GRPCPlugin, "cri")
	if err != nil {
		return nil, fmt.Errorf("unable to load CRI service base dependencies: %w", err)
	}
	criBase := criBasePlugin.(*base.CRIBase)

	// Get image service.
	criImagePlugin, err := ic.GetByID(plugin.GRPCPlugin, "cri-image-service")
	if err != nil {
		return nil, fmt.Errorf("unable to load CRI image service plugin dependency: %w", err)
	}

	var (
		s            server.CRIService
		imageService = criImagePlugin.(*images.CRIImageService)
	)

	if os.Getenv("DISABLE_CRI_SANDBOXES") == "" {
		log.G(ctx).Info("using CRI Sandbox server - use DISABLE_CRI_SANDBOXES=1 to fallback to legacy CRI")
		s, err = sbserver.NewCRIService(criBase.Config, imageService, criBase.Client, getNRIAPI(ic))
	} else {
		log.G(ctx).Info("using legacy CRI server")
		s, err = server.NewCRIService(criBase.Config, criBase.Client, getNRIAPI(ic))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create CRI service: %w", err)
	}

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
		pluginType = plugin.NRIApiPlugin
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
