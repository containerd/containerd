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
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/fsnotify/fsnotify"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/klog/v2"

	"github.com/containerd/containerd/v2/oci"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/pkg/cri/util"
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

	if c.ContainerdConfig.RuntimeConfigPath != "" {
		runtimeConfig := criconfig.NewRuntimeConfig(c.RuntimeConfigPath, c.DefaultRuntimeName, loadOCISpec)
		if err := runtimeConfig.Init(ctx); err != nil {
			return nil, fmt.Errorf("failed to init runtime config: %w", err)
		}
		c.CRIRc = runtimeConfig
	}

	if err := setGLogLevel(); err != nil {
		return nil, fmt.Errorf("failed to set glog level: %w", err)
	}

	ociSpec, err := loadBaseOCISpecs(&c)
	if err != nil {
		return nil, fmt.Errorf("failed to create load basic oci spec: %w", err)
	}

	base := &CRIBase{
		Config:       c,
		BaseOCISpecs: ociSpec,
	}
	if c.ContainerdConfig.RuntimeConfigPath != "" {
		if err := base.watchDefaultRuntimeNameValue(ctx); err != nil {
			return nil, fmt.Errorf("faild to watch default runtime name: %w", err)
		}
	}
	return base, nil
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

func (cb *CRIBase) GetOCISpec(baseRuntimeSpec string) (*oci.Spec, bool) {
	if cb.Config.RuntimeConfigPath != "" {
		ociSpec, _ := cb.Config.CRIRc.GetOCISpec(baseRuntimeSpec)
		return ociSpec, true
	}
	v, ok := cb.BaseOCISpecs[baseRuntimeSpec]
	return v, ok
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

type configPathKey struct{}

// WithConfigPath set config
func WithConfigPath(ctx context.Context, configPath string) context.Context {
	ctx = context.WithValue(ctx, configPathKey{}, configPath) // set our key for namespace
	return ctx
}

// WatchDefaultRuntimeNameValue watch config file, only handler default_runtime_name field change.
func (cb *CRIBase) watchDefaultRuntimeNameValue(ctx context.Context) error {
	configPathValue := ctx.Value(configPathKey{})
	if configPathValue == nil || configPathValue == "" {
		return nil
	}
	configPath := configPathValue.(string)
	notifyFile, err := util.NewNotifyFile()
	if err != nil {
		log.G(ctx).Errorf("create fsnotify watcher error %+v", err)
		return err
	}
	return notifyFile.WatchFile(ctx, configPath, cb.notifyEventFunc)
}

func (cb *CRIBase) notifyEventFunc(ctx context.Context, event fsnotify.Event) error {
	name := event.Name
	_, fileName := filepath.Split(name)
	if fileName != "config.toml" {
		return nil
	}
	if event.Op&fsnotify.Chmod == fsnotify.Chmod || event.Op&fsnotify.Rename == fsnotify.Rename {
		return nil
	}

	if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
		file, err := os.Open(event.Name)
		if err != nil {
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "default_runtime_name") {
				defaultRuntimeName := strings.TrimSpace(strings.ReplaceAll(strings.Split(scanner.Text(), "=")[1], "\"", ""))
				if cb.Config.DefaultRuntimeName == defaultRuntimeName {
					break
				}
				log.G(ctx).Infof("watch default_runtime_name having change, old value %s, new value %s", cb.Config.DefaultRuntimeName, defaultRuntimeName)
				cb.Config.DefaultRuntimeName = defaultRuntimeName
			}
		}
	}
	return nil
}
