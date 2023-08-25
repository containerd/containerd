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
	"fmt"
	"os"
	"context"
	"path/filepath"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/cri/nri"
	"github.com/containerd/containerd/pkg/cri/sbserver"
	nriservice "github.com/containerd/containerd/pkg/nri"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/klog/v2"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/pkg/cri/server"
)

// Register CRI service plugin
func init() {
	config := criconfig.DefaultConfig()
	plugin.Register(&plugin.Registration{
		Type:   plugin.GRPCPlugin,
		ID:     "cri",
		Config: &config,
		Requires: []plugin.Type{
			plugin.EventPlugin,
			plugin.ServicePlugin,
			plugin.NRIApiPlugin,
		},
		InitFn: initCRIService,
	})
}

func initCRIService(ic *plugin.InitContext) (interface{}, error) {
	ready := ic.RegisterReadiness()
	ic.Meta.Platforms = []imagespec.Platform{platforms.DefaultSpec()}
	ic.Meta.Exports = map[string]string{"CRIVersion": constants.CRIVersion}
	ctx := ic.Context
	pluginConfig := ic.Config.(*criconfig.PluginConfig)
	if err := criconfig.ValidatePluginConfig(ctx, pluginConfig); err != nil {
		return nil, fmt.Errorf("invalid plugin config: %w", err)
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
		return nil, fmt.Errorf("failed to set glog level: %w", err)
	}

	log.G(ctx).Info("Connect containerd service")
	client, err := containerd.New(
		"",
		containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
		containerd.WithDefaultPlatform(platforms.Default()),
		containerd.WithInMemoryServices(ic),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd client: %w", err)
	}

	// guestPlatform in runtime struct for a runtime helps to support pulling of imager
	// per runtime class. 
	// guestPlatform is used to specify an alternate platform to use with platform matcher
	// so that different versions of the same image can be pulled for different runtime handlers.
	// guestPlatform.OS and guestPlatform.Architecture are compusory fields to be specified and
	// if platform is windows, OSVersion needs to be specified too.
	// Overriding the host's default platform matcher with guestPlatform is not allowed for
	// windows process isolation as exact OSVersion match between host and guest is required.
	platformMap := make(map[string]platforms.MatchComparer)
	for k, ociRuntime := range c.PluginConfig.ContainerdConfig.Runtimes {
		// consider guesPlatform values only if OS and Architecture are specified
		if ociRuntime.GuestPlatform.OS != "" &&  ociRuntime.GuestPlatform.Architecture != "" {
			// For windows: check if the runtime class has sandbox isolation field set and use
			// guestplatform for  platform matcher only if it is hyperV isolation. Process isolated
			// cases would still need to have exact match between host and guest OS versions.
			if ociRuntime.Type == server.RuntimeRunhcsV1 {
				runtimeOpts, err := server.GenerateRuntimeOptions(ociRuntime)
				if err != nil {
					return nil, fmt.Errorf("Failed to get runtime options for runtime: %v", k)
				}
				if server.IsWindowsSandboxIsolation(ctx, runtimeOpts) {
					// ensure that OSVersion is mentioned for windows runtime handlers
					if ociRuntime.GuestPlatform.OSVersion == "" {
						return nil, fmt.Errorf("guestPlatform.OSVersion needs to be specified for windows hyperV runtimes")
					}
					platformMap[k] = platforms.Only(ociRuntime.GuestPlatform)
				} else {
					// Fail initialization if guestPlatform was specified for process isolation.
					// For process isolated cases, we only allow the host's default platform matcher to
					// be used as exact version match between host and guest is required.
					// Rather than silently ignoring guestPlatform in this case and using the host's default
					// platform matcher, it would be better to explicitly throw an error here so that user can
					// remove guestPlatform field from containerd.toml   
					return nil, fmt.Errorf("GuestPlatform cannot override the host platform for process isolation")
				}
			} else {
				platformMap[k] = platforms.Only(ociRuntime.GuestPlatform)
			}
		} else {
			platformMap[k] = platforms.Only(platforms.DefaultSpec())
		}
	}

	var s server.CRIService
	if os.Getenv("ENABLE_CRI_SANDBOXES") != "" {
		log.G(ctx).Info("using experimental CRI Sandbox server - unset ENABLE_CRI_SANDBOXES to disable")
		s, err = sbserver.NewCRIService(c, client, platformMap, getNRIAPI(ic))
	} else {
		log.G(ctx).Info("using legacy CRI server")
		s, err = server.NewCRIService(c, client, platformMap, getNRIAPI(ic))
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

// Set glog level.
func setGLogLevel() error {
	l := log.GetLevel()
	fs := flag.NewFlagSet("klog", flag.PanicOnError)
	klog.InitFlags(fs)
	if err := fs.Set("logtostderr", "true"); err != nil {
		return err
	}
	switch l {
	case log.TraceLevel:
		return fs.Set("v", "5")
	case log.DebugLevel:
		return fs.Set("v", "4")
	case log.InfoLevel:
		return fs.Set("v", "2")
	default:
		// glog doesn't support other filters. Defaults to v=0.
	}
	return nil
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
