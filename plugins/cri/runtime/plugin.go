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

package runtime

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

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/constants"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/services/warning"
	"github.com/containerd/containerd/v2/version"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
)

func init() {
	config := criconfig.DefaultRuntimeConfig()

	// Base plugin that other CRI services depend on.
	registry.Register(&plugin.Registration{
		Type:   plugins.CRIServicePlugin,
		ID:     "runtime",
		Config: &config,
		Requires: []plugin.Type{
			plugins.WarningPlugin,
		},
		ConfigMigration: configMigration,
		InitFn:          initCRIRuntime,
	})
}

func initCRIRuntime(ic *plugin.InitContext) (interface{}, error) {
	ic.Meta.Platforms = []imagespec.Platform{platforms.DefaultSpec()}
	ic.Meta.Exports = map[string]string{"CRIVersion": constants.CRIVersion}
	ctx := ic.Context
	pluginConfig := ic.Config.(*criconfig.RuntimeConfig)
	if warnings, err := criconfig.ValidateRuntimeConfig(ctx, pluginConfig); err != nil {
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
		RuntimeConfig:      *pluginConfig,
		ContainerdRootDir:  containerdRootDir,
		ContainerdEndpoint: ic.Properties[plugins.PropertyGRPCAddress],
		RootDir:            rootDir,
		StateDir:           stateDir,
	}

	// Ignoring errors here; this should never fail.
	cfg, _ := json.Marshal(c)
	log.G(ctx).WithFields(log.Fields{"config": string(cfg)}).Info("starting cri plugin")

	if err := setGLogLevel(); err != nil {
		return nil, fmt.Errorf("failed to set glog level: %w", err)
	}

	ociSpec, err := loadBaseOCISpecs(&c)
	if err != nil {
		return nil, fmt.Errorf("failed to create load basic oci spec: %w", err)
	}

	return &runtime{
		config:       c,
		baseOCISpecs: ociSpec,
	}, nil
}

// runtime contains common dependencies for CRI's runtime, image, and podsandbox services.
type runtime struct {
	// Config contains all configurations.
	config criconfig.Config
	// BaseOCISpecs contains cached OCI specs loaded via `Runtime.BaseRuntimeSpec`
	baseOCISpecs map[string]*oci.Spec
}

func (r *runtime) Config() criconfig.Config {
	return r.config
}

func (r *runtime) LoadOCISpec(filename string) (*oci.Spec, error) {
	spec, ok := r.baseOCISpecs[filename]
	if !ok {
		// TODO: Load here or only allow preloading...
		return nil, errdefs.ErrNotFound
	}
	return spec, nil
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

func configMigration(ctx context.Context, configVersion int, pluginConfigs map[string]interface{}) error {
	if configVersion >= version.ConfigVersion {
		return nil
	}
	src, ok := pluginConfigs[string(plugins.GRPCPlugin)+".cri"].(map[string]interface{})
	if !ok {
		return nil
	}
	dst, ok := pluginConfigs[string(plugins.CRIServicePlugin)+".runtime"].(map[string]interface{})
	if !ok {
		dst = make(map[string]interface{})
	}
	migrateConfig(dst, src)
	pluginConfigs[string(plugins.CRIServicePlugin)+".runtime"] = dst
	return nil
}

func migrateConfig(dst, src map[string]interface{}) {
	for k, v := range src {
		switch k {
		case "cni":
			// skip (handled separately below)
			continue
		case "containerd":
			// skip (handled separately below)
			continue
		case
			"sandbox_image",
			"registry",
			"image_decryption",
			"max_concurrent_downloads",
			"image_pull_progress_timeout",
			"image_pull_with_sync_fs",
			"stats_collect_period":
			// skip (moved to cri image service plugin)
			continue
		case
			"disable_tcp_service",
			"stream_server_address",
			"stream_server_port",
			"stream_idle_timeout",
			"enable_tls_streaming",
			"x509_key_pair_streaming":
			// skip (moved to cri ServerConfig)
			continue
		default:
			if _, ok := dst[k]; !ok {
				dst[k] = v
			}
		}
	}

	if cniConf, ok := src["cni"].(map[string]interface{}); ok {
		newCniConf, ok := dst["cni"].(map[string]interface{})
		if !ok {
			newCniConf = map[string]interface{}{}
		}
		for k, v := range cniConf {
			switch k {
			case "bin_dir":
				// migrate `bin_dir` to `bin_dirs` only if `bin_dirs`
				// is not already set
				if binDirs, ok := newCniConf["bin_dirs"].([]string); !ok || len(binDirs) == 0 {
					newCniConf["bin_dirs"] = []string{v.(string)}
				}
			default:
				if _, ok := newCniConf[k]; !ok {
					newCniConf[k] = v
				}
			}
		}
		dst["cni"] = newCniConf
	}

	// migrate cri containerd configs
	containerdConf, ok := src["containerd"].(map[string]interface{})
	if !ok {
		return
	}
	newContainerdConf, ok := dst["containerd"].(map[string]interface{})
	if !ok {
		newContainerdConf = map[string]interface{}{}
	}
	for k, v := range containerdConf {
		switch k {
		case "snapshotter", "disable_snapshot_annotations", "discard_unpacked_layers":
			// skip (moved to cri image service plugin)
			continue
		default:
			if _, ok := newContainerdConf[k]; !ok {
				newContainerdConf[k] = v
			}
		}
	}
	dst["containerd"] = newContainerdConf

	// migrate runtimes configs
	runtimesConf, ok := newContainerdConf["runtimes"]
	if !ok {
		return
	}
	for _, v := range runtimesConf.(map[string]interface{}) {
		runtimeConf := v.(map[string]interface{})
		if sandboxMode, ok := runtimeConf["sandbox_mode"]; ok {
			if _, ok := runtimeConf["sandboxer"]; !ok {
				runtimeConf["sandboxer"] = sandboxMode
				delete(runtimeConf, "sandbox_mode")
			}
		}
	}
}
