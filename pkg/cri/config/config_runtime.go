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

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/log"
	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml/v2"
)

var (
	DefaultRuntimeConfigPath = defaults.DefaultConfigDir + "/runtimes"
	DefaultRuntimeConfigName = "config.toml"
)

// CRIRuntimeConfig can auto reload runtimes and BaseOCISpecs data
type CRIRuntimeConfig struct {
	runtimeRW sync.RWMutex
	specRW    sync.RWMutex

	// DefaultRuntimeName is the default runtime name to use from the runtimes table.
	DefaultRuntimeName string `toml:"default_runtime_name" json:"defaultRuntimeName"`

	// RuntimeConfigPath is a path to the root directory containing runtime
	// If ConfigPath is set, the config.toml about runtime config content are ignored.
	RuntimeConfigPath string `toml:"runtime_config_path" json:"configPath"`

	// Runtimes is a map from CRI RuntimeHandler strings, which specify types of runtime
	// configurations, to the matching configurations.
	Runtimes map[string]Runtime `toml:"runtimes" json:"runtimes"`

	// BaseOCISpecs contains cached OCI specs loaded via `Runtime.BaseRuntimeSpec`
	BaseOCISpecs map[string]*oci.Spec

	loadOCISpec func(filename string) (*oci.Spec, error)
}

func NewRuntimeConfig(runtimeConfigPath, defaultRuntimeName string, loadOCISpec func(filename string) (*oci.Spec, error)) *CRIRuntimeConfig {
	if runtimeConfigPath == "" {
		runtimeConfigPath = DefaultRuntimeConfigPath
	}
	return &CRIRuntimeConfig{
		DefaultRuntimeName: defaultRuntimeName,
		RuntimeConfigPath:  runtimeConfigPath,
		loadOCISpec:        loadOCISpec,
	}
}

// Init init read runtime config dir file and start watch file
func (rc *CRIRuntimeConfig) Init(ctx context.Context) error {
	if _, err := os.Stat(rc.RuntimeConfigPath); os.IsNotExist(err) {
		log.G(ctx).Errorf("can't find %s runtime config path", rc.RuntimeConfigPath)
		return nil
	}
	if rc.Runtimes == nil {
		rc.Runtimes = make(map[string]Runtime)
	}
	err := filepath.Walk(rc.RuntimeConfigPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.G(ctx).Errorf("iterate dir error: %+v", err)
			return err
		}
		if info.IsDir() {
			configFilePath := filepath.Join(path, DefaultRuntimeConfigName)
			if _, err = os.Stat(configFilePath); os.IsNotExist(err) {
				log.G(ctx).Warnf("runtime config file not found, path is %s", configFilePath)
				return nil
			}
			runtime, err := rc.loadRuntimeConfig(ctx, info.Name(), configFilePath)
			if err != nil {
				log.G(ctx).Errorf("load runtime config error %+v", err)
				return err
			}
			if err = rc.loadOCISpecBase(*runtime); err != nil {
				log.G(ctx).Errorf("load OCI Spec error %+v from %s", err, runtime.BaseRuntimeSpec)
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	notifyFile, err := util.NewNotifyFile()
	if err != nil {
		return err
	}

	if err = notifyFile.WatchDir(ctx, rc.RuntimeConfigPath, rc.NotifyEventFunc); err != nil {
		log.G(ctx).Errorf("add notify watch dir error %+v", err)
		return err
	}

	if err = validateRuntimeConfig(rc.DefaultRuntimeName, rc.Runtimes); err != nil {
		log.G(ctx).Errorf("validate runtime config error %+v", err)
		return err
	}
	return err
}

func (rc *CRIRuntimeConfig) NotifyEventFunc(ctx context.Context, event fsnotify.Event) error {
	name := event.Name
	if event.Op&fsnotify.Chmod == fsnotify.Chmod || event.Op&fsnotify.Rename == fsnotify.Rename {
		return nil
	}
	if event.Op&fsnotify.Remove == fsnotify.Remove {
		dirPath, fileName := filepath.Split(name)
		if fileName != DefaultRuntimeConfigName {
			return nil
		}
		runtimeHandler := filepath.Base(dirPath)
		rc.runtimeRW.Lock()
		defer rc.runtimeRW.Unlock()
		delete(rc.Runtimes, runtimeHandler)
		log.G(ctx).Warnf("config.toml file having delete, file path is: %s", name)
		return nil
	}

	if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
		fi, err := os.Stat(name)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		dirPath, fileName := filepath.Split(name)
		if fileName != DefaultRuntimeConfigName {
			return nil
		}
		runtimeHandler := filepath.Base(dirPath)
		runtime, err := rc.loadRuntimeConfig(ctx, runtimeHandler, name)
		if err != nil {
			log.G(ctx).Errorf("load runtime config error %+v", err)
			return err
		}
		if err = rc.loadOCISpecBase(*runtime); err != nil {
			log.G(ctx).Errorf("load OCI Spec error %+v from %s", err, runtime.BaseRuntimeSpec)
			return err
		}
	}
	return nil
}

func (rc *CRIRuntimeConfig) loadRuntimeConfig(ctx context.Context, handler, configPath string) (*Runtime, error) {
	rc.runtimeRW.Lock()
	defer rc.runtimeRW.Unlock()
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.G(ctx).Errorf("read runtime config file content error: %+v", err)
		return nil, err
	}
	var runtime Runtime
	if err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&runtime); err != nil {
		var serr *toml.StrictMissingError
		if errors.As(err, &serr) {
			for _, derr := range serr.Errors {
				log.G(ctx).WithFields(log.Fields{
					"key": strings.Join(derr.Key(), " "),
				}).WithError(err).Warn("Ignoring unknown key in TOML for plugin")
			}
			err = nil
		}
		if err != nil {
			return nil, err
		}
	}
	rc.Runtimes[handler] = runtime
	return &runtime, nil
}

func (rc *CRIRuntimeConfig) loadOCISpecBase(runtime Runtime) error {
	// TODO add watch BaseRuntimeSpec file
	if runtime.BaseRuntimeSpec == "" {
		return nil
	}
	// Don't load same file twice
	rc.specRW.Lock()
	defer rc.specRW.Unlock()
	if _, ok := rc.BaseOCISpecs[runtime.BaseRuntimeSpec]; ok {
		return nil
	}
	spec, err := rc.loadOCISpec(runtime.BaseRuntimeSpec)
	if err != nil {
		return fmt.Errorf("failed to load base OCI spec from file: %s: %w", runtime.BaseRuntimeSpec, err)
	}
	if spec.Process != nil && spec.Process.Capabilities != nil && len(spec.Process.Capabilities.Inheritable) > 0 {
		log.L.WithField("base_runtime_spec", runtime.BaseRuntimeSpec).Warn("Provided base runtime spec includes inheritable capabilities, which may be unsafe. See CVE-2022-24769 for more details.")
	}
	rc.BaseOCISpecs[runtime.BaseRuntimeSpec] = spec
	return nil
}

func (rc *CRIRuntimeConfig) GetRuntime(runtime string) (Runtime, bool) {
	rc.runtimeRW.RLock()
	defer rc.runtimeRW.RUnlock()
	v, ok := rc.Runtimes[runtime]
	return v, ok
}

func (rc *CRIRuntimeConfig) GetOCISpec(baseRuntimeSpec string) (*oci.Spec, bool) {
	rc.specRW.RLock()
	defer rc.specRW.RUnlock()
	v, ok := rc.BaseOCISpecs[baseRuntimeSpec]
	return v, ok
}
