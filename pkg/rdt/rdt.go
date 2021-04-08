// +build linux
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

package rdt

import (
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"

	"github.com/intel/goresctrl/pkg/rdt"
	"github.com/pkg/errors"
)

const (
	// ResctrlPrefix is the prefix used for class/closid directories under the resctrl filesystem
	ResctrlPrefix = ""
)

// Config contains the configuration of the RDT plugin
type Config struct {
	ConfigFile string `toml:"config_file" json:"configFile"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.InternalPlugin,
		ID:     "rdt",
		Config: &Config{},
		InitFn: initRdt,
	})
}

func initRdt(ic *plugin.InitContext) (interface{}, error) {
	if err := rdt.Initialize(ResctrlPrefix); err != nil {
		log.L.Infof("RDT is not enabled: %v", err)
		return nil, nil
	}

	config, ok := ic.Config.(*Config)
	if !ok {
		return nil, errors.New("invalid RDT configuration")
	}

	if config.ConfigFile == "" {
		log.L.Info("No RDT config file specified, RDT not configured")
		return nil, nil
	}

	if err := rdt.SetConfigFromFile(config.ConfigFile, true); err != nil {
		return nil, errors.Wrap(err, "configuring RDT failed")
	}

	return nil, nil

}
