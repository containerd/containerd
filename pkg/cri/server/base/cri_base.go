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

package base

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/klog/v2"

	"github.com/containerd/containerd/v2/oci"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	srvconfig "github.com/containerd/containerd/v2/services/server/config"
	"github.com/containerd/containerd/v2/services/warning"
)

// CRIBase contains common dependencies for CRI's runtime, image, and podsandbox services.
type CRIBase struct {
	// Config contains all configurations.
	Config criconfig.Config
	// BaseOCISpecs contains cached OCI specs loaded via `Runtime.BaseRuntimeSpec`
	BaseOCISpecs map[string]*oci.Spec
}

func init() {
	config := criconfig.DefaultConfig()

	// Base plugin that other CRI services depend on.
	registry.Register(&plugin.Registration{
		Type:   plugins.InternalPlugin,
		ID:     "cri",
		Config: &config,
		Requires: []plugin.Type{
			plugins.WarningPlugin,
		},
		ConfigMigration: func(ctx context.Context, version int, plugins map[string]interface{}) error {
			if version >= srvconfig.CurrentConfigVersion {
				return nil
			}
			c, ok := plugins["io.containerd.grpc.v1.cri"]
			if !ok {
				return nil
			}
			conf := c.(map[string]interface{})
			migrateConfig(conf)
			plugins["io.containerd.internal.v1.cri"] = conf
			return nil
		},
		InitFn: initCRIBase,
	})
}

func initCRIBase(ic *plugin.InitContext) (interface{}, error) {
	ic.Meta.Platforms = []imagespec.Platform{platforms.DefaultSpec()}
	ic.Meta.Exports = map[string]string{"CRIVersion": constants.CRIVersion}
	ctx := ic.Context
	pluginConfig := ic.Config.(*criconfig.PluginConfig)
	if warnings, err := criconfig.ValidatePluginConfig(ctx, pluginConfig); err != nil {
		return nil, fmt.Errorf("invalid plugin config: %w", err)
	} else if len(warnings) > 0 {
		ws, err := ic.GetSingle(plugins.WarningPlugin)
		if err != nil {
			return nil, err
		}
		warn := ws.(warning.Service)
		for _, w := range warnings {
			warn.Emit(ctx, w)
		}
	}

	// For backward compatibility, we have to keep the rootDir and stateDir the same as before.
	containerdRootDir := filepath.Dir(ic.Properties[plugins.PropertyRootDir])
	rootDir := filepath.Join(containerdRootDir, "io.containerd.grpc.v1.cri")
	containerdStateDir := filepath.Dir(ic.Properties[plugins.PropertyStateDir])
	stateDir := filepath.Join(containerdStateDir, "io.containerd.grpc.v1.cri")
	c := criconfig.Config{
		PluginConfig:       *pluginConfig,
		ContainerdRootDir:  containerdRootDir,
		ContainerdEndpoint: ic.Properties[plugins.PropertyGRPCAddress],
		RootDir:            rootDir,
		StateDir:           stateDir,
	}

	log.G(ctx).Infof("Start cri plugin with config %+v", c)

	if err := setGLogLevel(); err != nil {
		return nil, fmt.Errorf("failed to set glog level: %w", err)
	}

	ociSpec, err := loadBaseOCISpecs(&c)
	if err != nil {
		return nil, fmt.Errorf("failed to create load basic oci spec: %w", err)
	}

	return &CRIBase{
		Config:       c,
		BaseOCISpecs: ociSpec,
	}, nil
}

func loadBaseOCISpecs(config *criconfig.Config) (map[string]*oci.Spec, error) {
	specs := map[string]*oci.Spec{}
	for _, cfg := range config.Runtimes {
		if cfg.BaseRuntimeSpec == "" {
			continue
		}

		// Don't load same file twice
		if _, ok := specs[cfg.BaseRuntimeSpec]; ok {
			continue
		}

		spec, err := loadOCISpec(cfg.BaseRuntimeSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to load base OCI spec from file: %s: %w", cfg.BaseRuntimeSpec, err)
		}

		specs[cfg.BaseRuntimeSpec] = spec
	}

	return specs, nil
}

func loadOCISpec(filename string) (*oci.Spec, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open base OCI spec: %s: %w", filename, err)
	}
	defer file.Close()

	spec := oci.Spec{}
	if err := json.NewDecoder(file).Decode(&spec); err != nil {
		return nil, fmt.Errorf("failed to parse base OCI spec file: %w", err)
	}

	return &spec, nil
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

func migrateConfig(conf map[string]interface{}) {
	containerdConf, ok := conf["containerd"]
	if !ok {
		return
	}
	runtimesConf, ok := containerdConf.(map[string]interface{})["runtimes"]
	if !ok {
		return
	}
	for _, v := range runtimesConf.(map[string]interface{}) {
		runtimeConf := v.(map[string]interface{})
		if sandboxMode, ok := runtimeConf["sandbox_mode"]; ok {
			if _, ok := runtimeConf["sandboxer"]; !ok {
				runtimeConf["sandboxer"] = sandboxMode
			}
		}
	}
}
